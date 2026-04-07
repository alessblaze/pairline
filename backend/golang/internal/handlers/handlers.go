package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anish/omegle/backend/golang/internal/middleware"
	appredis "github.com/anish/omegle/backend/golang/internal/redis"
	"github.com/anish/omegle/backend/golang/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

func HealthHandlerGin(serviceName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":    "ok",
			"service":   serviceName,
			"timestamp": time.Now().UnixMilli(),
		})
	}
}

func LoginHandlerGin(c *gin.Context) {
	// Per-endpoint request size limit: prevent DoS through large payloads
	if c.Request.ContentLength > 4096 {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "Request too large"})
		return
	}

	var req struct {
		Username string `json:"username" binding:"required,max=100"`
		Password string `json:"password" binding:"required,max=255"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request properties"})
		return
	}

	db := storage.NewDatabase()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	var admin storage.AdminAccount
	result := db.GetDB().WithContext(ctx).Where("username = ? AND is_active = true", req.Username).First(&admin).Error

	if result != nil || !storage.CheckPasswordHash(req.Password, admin.PasswordHash) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Service misconfigured"})
		return
	}
	accessTTL := getEnvDurationMinutes("JWT_ACCESS_EXPIRATION_MINUTES", 15)
	refreshTTL := time.Duration(getEnvAsInt("JWT_EXPIRATION_HOURS", "8")) * time.Hour

	accessToken, refreshToken, csrfToken, err := issueAdminSession(admin.Username, admin.Role, jwtSecret, accessTTL, refreshTTL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate session token"})
		return
	}

	setAdminSessionCookies(c, accessToken, refreshToken, csrfToken, accessTTL, refreshTTL)
	writeAdminAuthResponse(c, admin.Username, admin.Role, csrfToken)
}

func RefreshAdminSessionHandlerGin(c *gin.Context) {
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Service misconfigured"})
		return
	}

	refreshCookie, err := c.Cookie(middleware.AdminRefreshCookieName)
	if err != nil || refreshCookie == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Refresh token required"})
		return
	}

	username, _, err := middleware.VerifyJWTWithType(refreshCookie, jwtSecret, middleware.TokenTypeRefresh)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid refresh token"})
		return
	}

	db := storage.NewDatabase()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	var admin storage.AdminAccount
	if err := db.GetDB().WithContext(ctx).
		Where("username = ? AND is_active = ?", username, true).
		First(&admin).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid refresh token"})
		return
	}

	accessTTL := getEnvDurationMinutes("JWT_ACCESS_EXPIRATION_MINUTES", 15)
	refreshTTL := time.Duration(getEnvAsInt("JWT_EXPIRATION_HOURS", "8")) * time.Hour

	accessToken, refreshToken, csrfToken, err := issueAdminSession(admin.Username, admin.Role, jwtSecret, accessTTL, refreshTTL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to refresh session"})
		return
	}

	setAdminSessionCookies(c, accessToken, refreshToken, csrfToken, accessTTL, refreshTTL)
	writeAdminAuthResponse(c, admin.Username, admin.Role, csrfToken)
}

func LogoutAdminSessionHandlerGin(c *gin.Context) {
	clearAdminSessionCookies(c)
	c.JSON(http.StatusOK, gin.H{"status": "logged_out"})
}

func GetReportsHandlerGin(c *gin.Context) {
	status := c.Query("status")
	limitStr := c.Query("limit")

	limit := 10
	if limitStr != "" {
		if limitStr == "all" {
			limit = 1000
		} else {
			if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
				if parsed > 1000 {
					limit = 1000
				} else {
					limit = parsed
				}
			}
		}
	}

	db := storage.NewDatabase()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	query := db.GetDB().WithContext(ctx).Model(&storage.Report{})

	if status == "pending" {
		query = query.Where("status = ?", "pending")
	} else if status == "decided" {
		query = query.Where("status IN ?", []string{"approved", "rejected"})
	}

	var reports []storage.Report
	result := query.Order("created_at DESC").Limit(limit).Find(&reports).Error

	if result != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch reports"})
		return
	}

	metrics, err := loadReportMetrics(ctx, db.GetDB())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch report metrics"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"reports": reports,
		"metrics": metrics,
	})
}

func UpdateReportHandlerGin(c *gin.Context) {
	id := c.Param("id")

	// Validate UUID format for report ID
	if !uuidRe.MatchString(id) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid report ID format"})
		return
	}

	username, ok := getContextString(c, "username")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var req struct {
		Status string `json:"status"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	if req.Status != "approved" && req.Status != "rejected" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid status"})
		return
	}

	db := storage.NewDatabase()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	reviewedAt := time.Now()
	tx := db.GetDB().WithContext(ctx).Model(&storage.Report{}).Where("id = ? AND status = ?", id, "pending").Updates(map[string]interface{}{
		"status":               req.Status,
		"reviewed_by_username": username,
		"reviewed_at":          reviewedAt,
	})

	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update report"})
		return
	}

	if tx.RowsAffected == 0 {
		var existingCount int64
		if err := db.GetDB().WithContext(ctx).Model(&storage.Report{}).Where("id = ?", id).Count(&existingCount).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update report"})
			return
		}
		if existingCount > 0 {
			c.JSON(http.StatusConflict, gin.H{"error": "Report has already been reviewed"})
			return
		}

		c.JSON(http.StatusNotFound, gin.H{"error": "Report not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "updated",
	})
}

func CreateReportHandlerGin(redisClient redis.UniversalClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Per-endpoint request size limit: reports with chat logs can be larger but still bounded
		if c.Request.ContentLength > 262144 { // 256 KB
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "Request too large"})
			return
		}

		var req struct {
			ReporterSessionID string          `json:"reporter_session_id" binding:"required,uuid"`
			ReporterToken     string          `json:"reporter_token" binding:"required,max=128"`
			ReportedSessionID string          `json:"reported_session_id" binding:"required,uuid"`
			Reason            string          `json:"reason" binding:"required,max=100"`
			Description       string          `json:"description" binding:"max=500"`
			ChatLog           json.RawMessage `json:"chat_log"`
		}

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request parameters"})
			return
		}

		if req.ReportedSessionID == req.ReporterSessionID {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot report your own session"})
			return
		}

		req.Reason = stripHTML(req.Reason)
		req.Description = stripHTML(req.Description)
		if req.Reason == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Reason cannot be empty"})
			return
		}

		db := storage.NewDatabase()
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()

		if !verifySessionToken(ctx, redisClient, req.ReporterSessionID, req.ReporterToken) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid session token"})
			return
		}

		if !sessionCanReportPeer(ctx, redisClient, req.ReporterSessionID, req.ReportedSessionID) {
			c.JSON(http.StatusForbidden, gin.H{"error": "Reports are only allowed for your current or recent chat partner"})
			return
		}

		reporterRoute, reporterRouteErr := appredis.ResolveSessionRouteForReport(ctx, redisClient, req.ReporterSessionID)
		reportedRoute, reportedRouteErr := appredis.ResolveSessionRouteForReport(ctx, redisClient, req.ReportedSessionID)

		rawReporterIP := ""
		if reporterRouteErr == nil {
			rawReporterIP, _ = redisClient.Get(ctx, appredis.SessionIPKey(req.ReporterSessionID, reporterRoute)).Result()
		}

		rawReportedIP := ""
		if reportedRouteErr == nil {
			rawReportedIP, _ = redisClient.Get(ctx, appredis.SessionIPKey(req.ReportedSessionID, reportedRoute)).Result()
		}
		if rawReportedIP == "" {
			rawReportedIP, _ = redisClient.Get(ctx, appredis.SessionIPLocatorKey(req.ReportedSessionID)).Result()
		}

		reporterIP := normalizeIP(rawReporterIP)
		reportedIP := normalizeIP(rawReportedIP)

		if reporterIP == "" {
			reporterIP = getRequestClientIP(c)
		}

		chatLogStr, err := normalizeChatLog(req.ChatLog)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		report := storage.Report{
			ReporterSessionID: req.ReporterSessionID,
			ReportedSessionID: req.ReportedSessionID,
			ReporterIP:        reporterIP,
			ReportedIP:        reportedIP,
			Reason:            req.Reason,
			Description:       req.Description,
			ChatLog:           chatLogStr,
			Status:            "pending",
			CreatedAt:         time.Now(),
		}

		if err := db.GetDB().WithContext(ctx).Create(&report).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create report"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "created",
		})
	}
}

func CreateBanHandlerGin(redisClient *appredis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Per-endpoint request size limit: prevent DoS through large payloads
		if c.Request.ContentLength > 4096 {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "Request too large"})
			return
		}

		var req struct {
			SessionID  string `json:"session_id" binding:"omitempty,uuid"`
			IP         string `json:"ip" binding:"omitempty,max=45"`
			Reason     string `json:"reason" binding:"required,max=200"`
			ExpiryDate string `json:"expiry_date"`
		}

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request parameters"})
			return
		}

		if req.SessionID == "" && req.IP == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing session_id or ip"})
			return
		}

		if req.IP != "" && net.ParseIP(req.IP) == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid IP address format"})
			return
		}

		req.Reason = stripHTML(req.Reason)
		if req.Reason == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Reason cannot be empty"})
			return
		}

		db := storage.NewDatabase()

		username, ok := getContextString(c, "username")
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()

		var ipAddress string
		var sessionID string
		var expiresAt *time.Time

		if req.SessionID != "" {
			sessionID = req.SessionID
			ipAddress = resolveBanIPAddress(ctx, redisClient.GetClient(), req.SessionID, req.IP)
		} else {
			ipAddress = normalizeIP(req.IP)
			if ipAddress == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid IP address format"})
				return
			}
		}

		if ipAddress != "" {
			if isPrivateOrLocalIP(ipAddress) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot ban internal/local IP address"})
				return
			}
		}

		if req.ExpiryDate != "" {
			parsedExpiry, err := time.Parse(time.RFC3339, req.ExpiryDate)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid expiry_date"})
				return
			}

			if !parsedExpiry.After(time.Now()) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "expiry_date must be in the future"})
				return
			}

			if time.Until(parsedExpiry) > 365*24*time.Hour {
				c.JSON(http.StatusBadRequest, gin.H{"error": "expiry_date cannot exceed 1 year"})
				return
			}

			expiresAt = &parsedExpiry
		}

		var (
			existingBan   storage.Ban
			createdBan    storage.Ban
			alreadyBanned bool
		)

		err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := lockBanTargets(tx, sessionID, ipAddress); err != nil {
				return err
			}

			lookup := activeBanLookup(tx, sessionID, ipAddress, time.Now())
			if err := lookup.Order("created_at DESC").First(&existingBan).Error; err == nil {
				alreadyBanned = true
				return nil
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}

			createdBan = storage.Ban{
				SessionID:        sessionID,
				IPAddress:        ipAddress,
				Reason:           req.Reason,
				BannedByUsername: username,
				CreatedAt:        time.Now(),
				ExpiresAt:        expiresAt,
				IsActive:         true,
			}

			return tx.Create(&createdBan).Error
		})

		if err == nil && alreadyBanned {
			c.JSON(http.StatusOK, gin.H{
				"status": "already_banned",
				"ban_id": existingBan.ID,
			})
			return
		}

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create ban"})
			return
		}

		redisTTL := time.Duration(0)
		if expiresAt != nil {
			redisTTL = time.Until(*expiresAt)
		}
		redisPropagationFailed := false

		if sessionID != "" {
			err := appredis.SetIndexedValue(
				ctx,
				redisClient.GetClient(),
				appredis.BanIndexKey(),
				appredis.BanSessionKey(sessionID),
				req.Reason,
				redisTTL,
			)
			if err != nil {
				log.Printf("Failed to store ban in Redis: %v", err)
				redisPropagationFailed = true
			}

			err = redisClient.PublishBanAction(ctx, sessionID, ipAddress, req.Reason)
			if err != nil {
				log.Printf("Failed to publish ban action: %v", err)
				redisPropagationFailed = true
			}
		}

		if ipAddress != "" {
			err := appredis.SetIndexedValue(
				ctx,
				redisClient.GetClient(),
				appredis.BanIndexKey(),
				appredis.BanIPKey(ipAddress),
				req.Reason,
				redisTTL,
			)
			if err != nil {
				log.Printf("Failed to store IP ban in Redis: %v", err)
				redisPropagationFailed = true
			}

			err = redisClient.PublishBanIPAction(ctx, ipAddress, req.Reason)
			if err != nil {
				log.Printf("Failed to publish ban IP action: %v", err)
				redisPropagationFailed = true
			}
		}

		reviewedAt := time.Now()
		reportUpdate := db.GetDB().WithContext(ctx).Model(&storage.Report{}).
			Where("status = ?", "pending")

		switch {
		case sessionID != "" && ipAddress != "":
			reportUpdate = reportUpdate.Where("(reported_session_id = ? OR reported_ip = ?)", sessionID, ipAddress)
		case sessionID != "":
			reportUpdate = reportUpdate.Where("reported_session_id = ?", sessionID)
		case ipAddress != "":
			reportUpdate = reportUpdate.Where("reported_ip = ?", ipAddress)
		}

		if err := reportUpdate.Updates(map[string]interface{}{
			"status":               "approved",
			"reviewed_by_username": username,
			"reviewed_at":          reviewedAt,
		}).Error; err != nil {
			log.Printf("Failed to auto-approve related reports after ban: %v", err)
		}

		if redisPropagationFailed {
			c.JSON(http.StatusBadGateway, gin.H{
				"error": "Ban saved, but Redis propagation failed",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "banned",
			"ban_id": createdBan.ID,
		})
	}
}

func GetBansHandlerGin(c *gin.Context) {
	status := c.Query("status")
	limitStr := c.Query("limit")
	ipQuery := strings.ToLower(strings.TrimSpace(c.Query("ip")))

	if len(ipQuery) > 64 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ip query too long"})
		return
	}

	limit := 10
	if limitStr != "" {
		if limitStr == "all" {
			limit = 1000
		} else {
			if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
				if parsed > 1000 {
					limit = 1000
				} else {
					limit = parsed
				}
			}
		}
	}

	db := storage.NewDatabase()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	query := db.GetDB().WithContext(ctx).Model(&storage.Ban{})

	if status == "active" {
		query = query.Where("is_active = ?", true)
	} else if status == "inactive" {
		query = query.Where("is_active = ?", false)
	}

	if ipQuery != "" {
		query = query.Where("LOWER(ip_address) LIKE ?", "%"+ipQuery+"%")
	}

	var bans []storage.Ban
	result := query.Order("created_at DESC").Limit(limit).Find(&bans).Error

	if result != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch bans"})
		return
	}

	metrics, err := loadBanMetrics(ctx, db.GetDB())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch ban metrics"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"bans":    bans,
		"metrics": metrics,
	})
}

func DeleteBanHandlerGin(redisClient *appredis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		banIdentifier := c.Param("session_id")

		// Validate that banIdentifier is a valid UUID to prevent injection
		if !uuidRe.MatchString(banIdentifier) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ban identifier format"})
			return
		}

		db := storage.NewDatabase()

		username, ok := getContextString(c, "username")
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		now := time.Now()
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()

		var (
			ban                 storage.Ban
			remainingSessionBan *storage.Ban
			remainingIPBan      *storage.Ban
		)

		err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			result := tx.Where("id = ? AND is_active = ?", banIdentifier, true).First(&ban).Error

			if errors.Is(result, gorm.ErrRecordNotFound) {
				result = tx.Where("session_id = ? AND is_active = ?", banIdentifier, true).
					Order("created_at DESC").
					First(&ban).Error
			}

			if result != nil {
				return result
			}

			if err := lockBanTargets(tx, ban.SessionID, ban.IPAddress); err != nil {
				return err
			}

			if err := tx.Model(&storage.Ban{}).
				Where("id = ? AND is_active = ?", ban.ID, true).
				Updates(map[string]interface{}{
					"is_active":            false,
					"unbanned_at":          now,
					"unbanned_by_username": username,
				}).Error; err != nil {
				return err
			}

			if ban.SessionID != "" {
				if activeBan, err := latestActiveBan(tx, "session_id", ban.SessionID, now); err != nil {
					return err
				} else {
					remainingSessionBan = activeBan
				}
			}

			if ban.IPAddress != "" {
				if activeBan, err := latestActiveBan(tx, "ip_address", ban.IPAddress, now); err != nil {
					return err
				} else {
					remainingIPBan = activeBan
				}
			}

			return nil
		})

		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Ban not found"})
			return
		}

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to unban"})
			return
		}
		redisPropagationFailed := false

		if ban.SessionID != "" {
			if remainingSessionBan != nil {
				if err := appredis.SetIndexedValue(
					ctx,
					redisClient.GetClient(),
					appredis.BanIndexKey(),
					appredis.BanSessionKey(ban.SessionID),
					remainingSessionBan.Reason,
					redisBanTTL(*remainingSessionBan),
				); err != nil {
					log.Printf("Failed to refresh session ban in Redis: %v", err)
					redisPropagationFailed = true
				}
			} else {
				err := appredis.DeleteIndexedKey(
					ctx,
					redisClient.GetClient(),
					appredis.BanIndexKey(),
					appredis.BanSessionKey(ban.SessionID),
				)
				if err != nil {
					log.Printf("Failed to delete ban from Redis: %v", err)
					redisPropagationFailed = true
				}

				err = redisClient.PublishUnbanAction(ctx, ban.SessionID, ban.IPAddress)
				if err != nil {
					log.Printf("Failed to publish unban action: %v", err)
					redisPropagationFailed = true
				}
			}
		}

		if ban.IPAddress != "" {
			if remainingIPBan != nil {
				if err := appredis.SetIndexedValue(
					ctx,
					redisClient.GetClient(),
					appredis.BanIndexKey(),
					appredis.BanIPKey(ban.IPAddress),
					remainingIPBan.Reason,
					redisBanTTL(*remainingIPBan),
				); err != nil {
					log.Printf("Failed to refresh IP ban in Redis: %v", err)
					redisPropagationFailed = true
				}
			} else {
				err := appredis.DeleteIndexedKey(
					ctx,
					redisClient.GetClient(),
					appredis.BanIndexKey(),
					appredis.BanIPKey(ban.IPAddress),
				)
				if err != nil {
					log.Printf("Failed to delete IP ban from Redis: %v", err)
					redisPropagationFailed = true
				}

				err = redisClient.PublishUnbanIPAction(ctx, ban.IPAddress)
				if err != nil {
					log.Printf("Failed to publish unban IP action: %v", err)
					redisPropagationFailed = true
				}
			}
		}

		if redisPropagationFailed {
			c.JSON(http.StatusBadGateway, gin.H{
				"error": "Unban saved, but Redis propagation failed",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "unbanned",
			"ban_id": ban.ID,
		})
	}
}

func ListAdminAccountsHandlerGin(c *gin.Context) {
	limit := 25
	if limitStr := strings.TrimSpace(c.Query("limit")); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
			if parsed > 100 {
				limit = 100
			} else {
				limit = parsed
			}
		}
	}

	page := 1
	if pageStr := strings.TrimSpace(c.Query("page")); pageStr != "" {
		if parsed, err := strconv.Atoi(pageStr); err == nil && parsed > 0 {
			page = parsed
		}
	}

	searchQuery := strings.TrimSpace(c.Query("q"))
	if len(searchQuery) > 50 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "search query too long"})
		return
	}

	db := storage.NewDatabase()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	query := db.GetDB().WithContext(ctx).Model(&storage.AdminAccount{})
	if searchQuery != "" {
		query = query.Where("LOWER(username) LIKE ?", "%"+strings.ToLower(searchQuery)+"%")
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to count admin accounts"})
		return
	}

	var accounts []storage.AdminAccount
	offset := (page - 1) * limit
	if err := query.
		Select("id", "username", "role", "created_at", "created_by_username", "is_active").
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&accounts).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch admin accounts"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"accounts": accounts,
		"total":    total,
		"page":     page,
		"limit":    limit,
	})
}

func CreateAdminHandlerGin(c *gin.Context) {
	// Per-endpoint request size limit: prevent DoS through large payloads
	if c.Request.ContentLength > 4096 {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "Request too large"})
		return
	}

	var req struct {
		Username string `json:"username" binding:"required,max=50"`
		Password string `json:"password" binding:"required,min=8,max=128"`
		Role     string `json:"role" binding:"required,oneof=admin root moderator"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request parameters"})
		return
	}

	if !usernameRe.MatchString(req.Username) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Username must be 3-50 characters and contain only letters, numbers, hyphens, or underscores"})
		return
	}

	req.Username = stripHTML(req.Username)
	currentUsername, ok := getContextString(c, "username")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	currentRole, ok := getContextString(c, "role")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	if !canCreateAdminRole(currentRole, req.Role) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Insufficient privileges to assign this role"})
		return
	}

	db := storage.NewDatabase()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	var existingAdmin storage.AdminAccount
	result := db.GetDB().WithContext(ctx).Where("username = ?", req.Username).First(&existingAdmin).Error
	if result == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Admin already exists"})
		return
	}
	if !errors.Is(result, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing admin"})
		return
	}

	hash, err := storage.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to securely hash password"})
		return
	}

	admin := storage.AdminAccount{
		Username:          req.Username,
		PasswordHash:      hash,
		Role:              req.Role,
		IsActive:          true,
		CreatedAt:         time.Now(),
		CreatedByUsername: currentUsername,
	}

	result = db.GetDB().WithContext(ctx).Create(&admin).Error
	if result != nil {
		if isDuplicateKeyError(result) {
			c.JSON(http.StatusConflict, gin.H{"error": "Admin already exists"})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create admin"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "created",
	})
}

func DeleteAdminHandlerGin(c *gin.Context) {
	username := c.Param("username")

	// Validate username format to prevent injection via path parameter
	if !usernameRe.MatchString(username) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid username format"})
		return
	}

	currentUsername, ok := getContextString(c, "username")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	currentRole, ok := getContextString(c, "role")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	if username == currentUsername {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot delete your own account"})
		return
	}

	db := storage.NewDatabase()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	var targetAdmin storage.AdminAccount
	if err := db.GetDB().WithContext(ctx).Where("username = ?", username).First(&targetAdmin).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Admin account not found"})
		return
	}

	if currentRole != "root" && (targetAdmin.Role == "root" || targetAdmin.Role == "admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Insufficient privileges to delete this admin account"})
		return
	}

	result := db.GetDB().WithContext(ctx).Delete(&targetAdmin).Error
	if result != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete admin account"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "deleted",
	})
}

func HealthHandler(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "ok",
		"service":   "omegle-go-service",
		"timestamp": time.Now().UnixMilli(),
	})
}

func sendError(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": message,
		"code":  status,
	})
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key, defaultValue string) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	if intVal, err := strconv.Atoi(defaultValue); err == nil {
		return intVal
	}
	return 0
}

func getEnvDurationMinutes(key string, defaultMinutes int) time.Duration {
	return time.Duration(getEnvAsInt(key, strconv.Itoa(defaultMinutes))) * time.Minute
}

func generateOpaqueToken() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	alphabetLen := big.NewInt(int64(len(alphabet)))
	out := make([]byte, 32)
	for i := range out {
		idx, err := rand.Int(rand.Reader, alphabetLen)
		if err != nil {
			log.Fatalf("CRITICAL: crypto/rand failure, cannot generate secure tokens: %v", err)
		}
		out[i] = alphabet[idx.Int64()]
	}

	return string(out)
}

func issueAdminSession(username, role, jwtSecret string, accessTTL, refreshTTL time.Duration) (string, string, string, error) {
	accessToken, err := middleware.GenerateJWTWithType(username, role, middleware.TokenTypeAccess, jwtSecret, accessTTL)
	if err != nil {
		return "", "", "", err
	}

	refreshToken, err := middleware.GenerateJWTWithType(username, role, middleware.TokenTypeRefresh, jwtSecret, refreshTTL)
	if err != nil {
		return "", "", "", err
	}

	return accessToken, refreshToken, generateOpaqueToken(), nil
}

func setAdminSessionCookies(c *gin.Context, accessToken, refreshToken, csrfToken string, accessTTL, refreshTTL time.Duration) {
	isSecure := c.Request.TLS != nil || c.Request.Header.Get("X-Forwarded-Proto") == "https"
	if isSecure {
		c.SetSameSite(http.SameSiteNoneMode)
	} else {
		c.SetSameSite(http.SameSiteLaxMode)
	}

	c.SetCookie(middleware.AdminAccessCookieName, accessToken, int(accessTTL.Seconds()), "/", "", isSecure, true)
	c.SetCookie(middleware.AdminRefreshCookieName, refreshToken, int(refreshTTL.Seconds()), "/", "", isSecure, true)
	c.SetCookie(middleware.AdminCSRFCookieName, csrfToken, int(refreshTTL.Seconds()), "/", "", isSecure, false)
	c.SetCookie(middleware.LegacyAdminAccessCookieName, "", -1, "/", "", isSecure, true)
}

func clearAdminSessionCookies(c *gin.Context) {
	isSecure := c.Request.TLS != nil || c.Request.Header.Get("X-Forwarded-Proto") == "https"
	if isSecure {
		c.SetSameSite(http.SameSiteNoneMode)
	} else {
		c.SetSameSite(http.SameSiteLaxMode)
	}

	c.SetCookie(middleware.AdminAccessCookieName, "", -1, "/", "", isSecure, true)
	c.SetCookie(middleware.AdminRefreshCookieName, "", -1, "/", "", isSecure, true)
	c.SetCookie(middleware.AdminCSRFCookieName, "", -1, "/", "", isSecure, false)
	c.SetCookie(middleware.LegacyAdminAccessCookieName, "", -1, "/", "", isSecure, true)
}

func writeAdminAuthResponse(c *gin.Context, username, role, csrfToken string) {
	c.Header("X-CSRF-Token", csrfToken)
	c.JSON(http.StatusOK, gin.H{
		"username":   username,
		"role":       role,
		"csrf_token": csrfToken,
	})
}

func getContextString(c *gin.Context, key string) (string, bool) {
	value, exists := c.Get(key)
	if !exists {
		return "", false
	}

	str, ok := value.(string)
	return str, ok && str != ""
}

func canCreateAdminRole(currentRole, targetRole string) bool {
	switch currentRole {
	case "root":
		return targetRole == "moderator" || targetRole == "admin" || targetRole == "root"
	case "admin":
		return targetRole == "moderator"
	default:
		return false
	}
}

func verifySessionToken(ctx context.Context, redisClient redis.UniversalClient, sessionID, providedToken string) bool {
	route, err := appredis.ResolveSessionRouteForReport(ctx, redisClient, sessionID)
	if err != nil {
		return false
	}

	expectedToken, err := redisClient.Get(ctx, appredis.SessionTokenKey(sessionID, route)).Result()
	if err != nil || expectedToken == "" {
		return false
	}

	hash := sha256.Sum256([]byte(providedToken))
	providedHashHex := hex.EncodeToString(hash[:])

	return subtle.ConstantTimeCompare([]byte(expectedToken), []byte(providedHashHex)) == 1
}

func sessionCanReportPeer(ctx context.Context, redisClient redis.UniversalClient, reporterSessionID, reportedSessionID string) bool {
	route, err := appredis.ResolveSessionRouteForReport(ctx, redisClient, reporterSessionID)
	if err != nil {
		return false
	}

	keys := []string{
		appredis.MatchKey(reporterSessionID, route),
		appredis.RecentMatchKey(reporterSessionID, route),
	}

	for _, key := range keys {
		peerID, err := redisClient.Get(ctx, key).Result()
		if err == nil && peerID == reportedSessionID {
			return true
		}
	}

	return false
}

func normalizeChatLog(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "[]", nil
	}

	type chatLogMessage struct {
		ID        string `json:"id"`
		Text      string `json:"text"`
		Sender    string `json:"sender"`
		Timestamp int64  `json:"timestamp"`
	}

	var messages []chatLogMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return "", errors.New("chat_log must be a JSON array of chat messages")
	}

	if len(messages) > 50 {
		return "", errors.New("chat_log exceeds maximum message count")
	}

	totalBytes := 0
	for i := range messages {
		if len(messages[i].Text) > 2_000 {
			return "", errors.New("chat_log contains an oversized message")
		}
		if messages[i].Sender != "me" && messages[i].Sender != "peer" && messages[i].Sender != "system" {
			return "", errors.New("chat_log contains an invalid sender value")
		}
		// Sanitize chat text to prevent stored XSS when rendered in admin panel
		messages[i].Text = stripHTML(messages[i].Text)
		totalBytes += len(messages[i].Text)
	}

	if totalBytes > 32_000 {
		return "", errors.New("chat_log exceeds maximum total size")
	}

	normalized, err := json.Marshal(messages)
	if err != nil {
		return "", errors.New("failed to encode chat_log")
	}

	return string(normalized), nil
}

func isPrivateOrLocalIP(raw string) bool {
	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return true
	}

	addr = addr.Unmap()

	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
		return true
	}

	cgnatPrefix := netip.MustParsePrefix("100.64.0.0/10")
	return cgnatPrefix.Contains(addr)
}

func getRequestClientIP(c *gin.Context) string {
	peerIP := parseRemoteIP(c.Request.RemoteAddr)
	if !isTrustedProxyIP(peerIP) {
		return peerIP
	}

	for _, candidate := range []string{
		c.GetHeader("CF-Connecting-IPv6"),
		c.GetHeader("CF-Connecting-IP"),
		c.GetHeader("X-Real-IP"),
		firstForwardedIP(c.GetHeader("X-Forwarded-For")),
	} {
		if normalized := normalizeIP(candidate); normalized != "" {
			return normalized
		}
	}

	return peerIP
}

func parseRemoteIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		host = strings.TrimSpace(remoteAddr)
	}

	if normalized := normalizeIP(host); normalized != "" {
		return normalized
	}

	return host
}

func firstForwardedIP(header string) string {
	if header == "" {
		return ""
	}

	return strings.TrimSpace(strings.Split(header, ",")[0])
}

func normalizeIP(raw string) string {
	if raw == "" {
		return ""
	}

	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}

	return addr.Unmap().String()
}

func isTrustedProxyIP(raw string) bool {
	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return false
	}

	addr = addr.Unmap()

	for _, prefix := range trustedProxyPrefixes() {
		if prefix.Contains(addr) {
			return true
		}
	}

	return false
}

var (
	trustedPrefixesOnce   sync.Once
	cachedTrustedPrefixes []netip.Prefix
)

func trustedProxyPrefixes() []netip.Prefix {
	trustedPrefixesOnce.Do(func() {
		raw := os.Getenv("TRUSTED_PROXY_CIDRS")
		if raw == "" {
			raw = "127.0.0.1/32,::1/128"
		}

		prefixes := make([]netip.Prefix, 0)
		for _, part := range strings.Split(raw, ",") {
			prefix, err := netip.ParsePrefix(strings.TrimSpace(part))
			if err == nil {
				prefixes = append(prefixes, prefix.Masked())
			}
		}
		cachedTrustedPrefixes = prefixes
	})

	return cachedTrustedPrefixes
}

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,50}$`)
var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

func stripHTML(s string) string {
	s = htmlTagRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "<", "")
	s = strings.ReplaceAll(s, ">", "")
	var clean strings.Builder
	for _, r := range s {
		if r == '\n' || r == '\t' || r >= 32 {
			clean.WriteRune(r)
		}
	}
	return strings.TrimSpace(clean.String())
}

func lockBanTargets(tx *gorm.DB, sessionID, ipAddress string) error {
	keys := make([]string, 0, 2)
	if sessionID != "" {
		keys = append(keys, appredis.BanSessionKey(sessionID))
	}
	if ipAddress != "" {
		keys = append(keys, appredis.BanIPKey(ipAddress))
	}
	sort.Strings(keys)

	for _, key := range keys {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", key).Error; err != nil {
			return err
		}
	}

	return nil
}

func lookupSessionIP(ctx context.Context, redisClient redis.UniversalClient, sessionID string) string {
	route, err := appredis.ResolveSessionRoute(ctx, redisClient, sessionID)
	if err != nil {
		return ""
	}

	raw, err := redisClient.Get(ctx, appredis.SessionIPKey(sessionID, route)).Result()
	if err != nil {
		return ""
	}

	return normalizeIP(raw)
}

func resolveBanIPAddress(ctx context.Context, redisClient redis.UniversalClient, sessionID, requestedIP string) string {
	if redisClient != nil {
		if ipAddress := lookupSessionIP(ctx, redisClient, sessionID); ipAddress != "" {
			return ipAddress
		}
	}

	return normalizeIP(requestedIP)
}

func activeBanLookup(tx *gorm.DB, sessionID, ipAddress string, now time.Time) *gorm.DB {
	lookup := tx.Where("is_active = ? AND (expires_at IS NULL OR expires_at > ?)", true, now)

	switch {
	case sessionID != "" && ipAddress != "":
		return lookup.Where("(session_id = ? OR ip_address = ?)", sessionID, ipAddress)
	case sessionID != "":
		return lookup.Where("session_id = ?", sessionID)
	default:
		return lookup.Where("ip_address = ?", ipAddress)
	}
}

// allowedBanLookupColumns prevents SQL injection by restricting the column
// parameter to a known-safe allowlist rather than accepting a raw WHERE clause.
var allowedBanLookupColumns = map[string]bool{"session_id": true, "ip_address": true}

func latestActiveBan(tx *gorm.DB, column string, value string, now time.Time) (*storage.Ban, error) {
	if !allowedBanLookupColumns[column] {
		return nil, errors.New("invalid ban lookup column")
	}
	var ban storage.Ban
	err := tx.Where(column+" = ? AND is_active = ? AND (expires_at IS NULL OR expires_at > ?)", value, true, now).
		Order("created_at DESC").
		First(&ban).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ban, nil
}

func redisBanTTL(ban storage.Ban) time.Duration {
	if ban.ExpiresAt == nil {
		return 0
	}

	ttl := time.Until(*ban.ExpiresAt)
	if ttl <= 0 {
		return time.Second
	}

	return ttl
}

func isDuplicateKeyError(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}

	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

type reportMetricRow struct {
	Status string
	Count  int64
}

func loadReportMetrics(ctx context.Context, db *gorm.DB) (map[string]int64, error) {
	rows := make([]reportMetricRow, 0, 3)
	if err := db.WithContext(ctx).
		Model(&storage.Report{}).
		Select("status, COUNT(*) AS count").
		Where("status IN ?", []string{"pending", "approved", "rejected"}).
		Group("status").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	metrics := map[string]int64{
		"pending":  0,
		"approved": 0,
		"rejected": 0,
	}

	for _, row := range rows {
		metrics[row.Status] = row.Count
	}

	return metrics, nil
}

type banMetricRow struct {
	IsActive bool
	Count    int64
}

func loadBanMetrics(ctx context.Context, db *gorm.DB) (map[string]int64, error) {
	rows := make([]banMetricRow, 0, 2)
	if err := db.WithContext(ctx).
		Model(&storage.Ban{}).
		Select("is_active, COUNT(*) AS count").
		Group("is_active").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	metrics := map[string]int64{
		"active":   0,
		"inactive": 0,
		"total":    0,
	}

	for _, row := range rows {
		if row.IsActive {
			metrics["active"] = row.Count
		} else {
			metrics["inactive"] = row.Count
		}
		metrics["total"] += row.Count
	}

	return metrics, nil
}
