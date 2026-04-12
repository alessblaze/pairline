// Pairline - Open Source Video Chat and Matchmaking
// Copyright (C) 2026 Albert Blasczykowski
// Aless Microsystems
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

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
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/anish/omegle/backend/golang/internal/middleware"
	"github.com/anish/omegle/backend/golang/internal/observability"
	appredis "github.com/anish/omegle/backend/golang/internal/redis"
	"github.com/anish/omegle/backend/golang/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"gorm.io/gorm"
)

func HealthHandlerGin(serviceName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)

		c.JSON(http.StatusOK, gin.H{
			"status":    "ok",
			"service":   serviceName,
			"timestamp": time.Now().UnixMilli(),
			"memory": gin.H{
				"heap_alloc_bytes":  mem.HeapAlloc,
				"heap_inuse_bytes":  mem.HeapInuse,
				"heap_sys_bytes":    mem.HeapSys,
				"stack_inuse_bytes": mem.StackInuse,
				"stack_sys_bytes":   mem.StackSys,
				"sys_bytes":         mem.Sys,
				"total_alloc_bytes": mem.TotalAlloc,
				"num_gc":            mem.NumGC,
				"goroutines":        runtime.NumGoroutine(),
			},
		})
	}
}

func LoginHandlerGin(c *gin.Context) {
	span := startHandlerSpan(c, "admin.login")
	defer span.End()

	// Per-endpoint request size limit: prevent DoS through large payloads
	if c.Request.ContentLength > 4096 {
		span.SetStatus(codes.Error, "request too large")
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "Request too large"})
		return
	}

	var req struct {
		Username string `json:"username" binding:"required,max=100"`
		Password string `json:"password" binding:"required,max=255"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "invalid request properties")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request properties"})
		return
	}
	span.SetAttributes(hashedAttribute("admin.user.ref", req.Username))

	db := storage.NewDatabase()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	var admin storage.AdminAccount
	result := db.GetDB().WithContext(ctx).Where("username = ? AND is_active = true", req.Username).First(&admin).Error

	if result != nil || !storage.CheckPasswordHash(req.Password, admin.PasswordHash) {
		span.SetStatus(codes.Error, "invalid credentials")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		span.SetStatus(codes.Error, "service misconfigured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Service misconfigured"})
		return
	}
	accessTTL := getEnvDurationMinutes("JWT_ACCESS_EXPIRATION_MINUTES", 15)
	refreshTTL := time.Duration(getEnvAsInt("JWT_EXPIRATION_HOURS", "8")) * time.Hour

	accessToken, refreshToken, csrfToken, err := issueAdminSession(admin.Username, admin.Role, jwtSecret, accessTTL, refreshTTL)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to generate session token")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate session token"})
		return
	}

	span.SetAttributes(attribute.String("admin.role", admin.Role))
	setAdminSessionCookies(c, accessToken, refreshToken, csrfToken, accessTTL, refreshTTL)
	writeAdminAuthResponse(c, admin.Username, admin.Role, csrfToken)
}

func RefreshAdminSessionHandlerGin(c *gin.Context) {
	span := startHandlerSpan(c, "admin.refresh")
	defer span.End()

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		span.SetStatus(codes.Error, "service misconfigured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Service misconfigured"})
		return
	}

	refreshCookie, err := c.Cookie(middleware.AdminRefreshCookieName)
	if err != nil || refreshCookie == "" {
		span.SetStatus(codes.Error, "refresh token required")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Refresh token required"})
		return
	}

	username, _, err := middleware.VerifyJWTWithType(refreshCookie, jwtSecret, middleware.TokenTypeRefresh)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "invalid refresh token")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid refresh token"})
		return
	}
	span.SetAttributes(hashedAttribute("admin.user.ref", username))

	db := storage.NewDatabase()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	var admin storage.AdminAccount
	if err := db.GetDB().WithContext(ctx).
		Where("username = ? AND is_active = ?", username, true).
		First(&admin).Error; err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "invalid refresh token")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid refresh token"})
		return
	}

	accessTTL := getEnvDurationMinutes("JWT_ACCESS_EXPIRATION_MINUTES", 15)
	refreshTTL := time.Duration(getEnvAsInt("JWT_EXPIRATION_HOURS", "8")) * time.Hour

	accessToken, refreshToken, csrfToken, err := issueAdminSession(admin.Username, admin.Role, jwtSecret, accessTTL, refreshTTL)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to refresh session")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to refresh session"})
		return
	}

	span.SetAttributes(attribute.String("admin.role", admin.Role))
	setAdminSessionCookies(c, accessToken, refreshToken, csrfToken, accessTTL, refreshTTL)
	writeAdminAuthResponse(c, admin.Username, admin.Role, csrfToken)
}

func LogoutAdminSessionHandlerGin(c *gin.Context) {
	span := startHandlerSpan(c, "admin.logout")
	defer span.End()

	clearAdminSessionCookies(c)
	c.JSON(http.StatusOK, gin.H{"status": "logged_out"})
}

func GetReportsHandlerGin(c *gin.Context) {
	span := startHandlerSpan(c, "admin.reports.list")
	defer span.End()

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
		span.RecordError(result)
		span.SetStatus(codes.Error, "failed to fetch reports")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch reports"})
		return
	}

	metrics, err := loadReportMetrics(ctx, db.GetDB())
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to fetch report metrics")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch report metrics"})
		return
	}

	span.SetAttributes(
		attribute.String("report.status_filter", status),
		attribute.Int("report.limit", limit),
		attribute.Int("report.count", len(reports)),
	)

	c.JSON(http.StatusOK, gin.H{
		"reports": reports,
		"metrics": metrics,
	})
}

func UpdateReportHandlerGin(c *gin.Context) {
	span := startHandlerSpan(c, "admin.reports.update")
	defer span.End()

	id := c.Param("id")
	span.SetAttributes(hashedAttribute("report.ref", id))

	// Validate UUID format for report ID
	if !uuidRe.MatchString(id) {
		span.SetStatus(codes.Error, "invalid report id format")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid report ID format"})
		return
	}

	username, ok := getContextString(c, "username")
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var req struct {
		Status string `json:"status"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "invalid request body")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	if req.Status != "approved" && req.Status != "rejected" {
		span.SetStatus(codes.Error, "invalid status")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid status"})
		return
	}
	span.SetAttributes(
		hashedAttribute("admin.user.ref", username),
		attribute.String("report.status", req.Status),
	)

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
		span.RecordError(tx.Error)
		span.SetStatus(codes.Error, "failed to update report")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update report"})
		return
	}

	if tx.RowsAffected == 0 {
		var existingCount int64
		if err := db.GetDB().WithContext(ctx).Model(&storage.Report{}).Where("id = ?", id).Count(&existingCount).Error; err != nil {
			span.RecordError(err)
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
		span := startHandlerSpan(c, "moderation.report.create")
		defer span.End()

		// Per-endpoint request size limit: reports with chat logs can be larger but still bounded
		if c.Request.ContentLength > 262144 { // 256 KB
			span.SetStatus(codes.Error, "request too large")
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
			span.RecordError(err)
			span.SetStatus(codes.Error, "invalid request parameters")
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request parameters"})
			return
		}
		span.SetAttributes(
			hashedAttribute("report.reporter.ref", req.ReporterSessionID),
			hashedAttribute("report.reported.ref", req.ReportedSessionID),
			attribute.Bool("report.description_present", strings.TrimSpace(req.Description) != ""),
		)

		if req.ReportedSessionID == req.ReporterSessionID {
			span.SetStatus(codes.Error, "self-report rejected")
			c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot report your own session"})
			return
		}

		req.Reason = stripHTML(req.Reason)
		req.Description = stripHTML(req.Description)
		if req.Reason == "" {
			span.SetStatus(codes.Error, "empty reason")
			c.JSON(http.StatusBadRequest, gin.H{"error": "Reason cannot be empty"})
			return
		}

		db := storage.NewDatabase()
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()

		if !verifySessionToken(ctx, redisClient, req.ReporterSessionID, req.ReporterToken) {
			span.SetStatus(codes.Error, "invalid session token")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid session token"})
			return
		}

		if !sessionCanReportPeer(ctx, redisClient, req.ReporterSessionID, req.ReportedSessionID) {
			span.SetStatus(codes.Error, "report peer not allowed")
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
			span.RecordError(err)
			span.SetStatus(codes.Error, "invalid chat log")
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
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to create report")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create report"})
			return
		}
		observability.RecordBusinessEvent(
			c.Request.Context(),
			"report.created",
			attribute.Bool("report.chat_log_present", chatLogStr != "[]"),
		)

		c.JSON(http.StatusOK, gin.H{
			"status": "created",
		})
	}
}

func CreateBanHandlerGin(redisClient *appredis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		span := startHandlerSpan(c, "moderation.ban.create")
		defer span.End()

		// Per-endpoint request size limit: prevent DoS through large payloads
		if c.Request.ContentLength > 4096 {
			span.SetStatus(codes.Error, "request too large")
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "Request too large"})
			return
		}

		var req struct {
			SessionID  string `json:"session_id" binding:"omitempty,uuid"`
			IP         string `json:"ip" binding:"omitempty,max=45"`
			ReportID   string `json:"report_id" binding:"omitempty,uuid"`
			Reason     string `json:"reason" binding:"required,max=200"`
			ExpiryDate string `json:"expiry_date"`
		}

		if err := c.ShouldBindJSON(&req); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "invalid request parameters")
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request parameters"})
			return
		}

		if req.SessionID == "" && req.IP == "" {
			span.SetStatus(codes.Error, "missing ban target")
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing session_id or ip"})
			return
		}

		if req.IP != "" && net.ParseIP(req.IP) == nil {
			span.SetStatus(codes.Error, "invalid ip address format")
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid IP address format"})
			return
		}

		req.Reason = stripHTML(req.Reason)
		if req.Reason == "" {
			span.SetStatus(codes.Error, "empty ban reason")
			c.JSON(http.StatusBadRequest, gin.H{"error": "Reason cannot be empty"})
			return
		}
		span.SetAttributes(
			hashedAttribute("ban.session.ref", req.SessionID),
			attribute.Bool("ban.ip_provided", strings.TrimSpace(req.IP) != ""),
			attribute.Bool("ban.reason_present", req.Reason != ""),
		)

		db := storage.NewDatabase()

		username, ok := getContextString(c, "username")
		if !ok {
			span.SetStatus(codes.Error, "unauthorized")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		span.SetAttributes(hashedAttribute("admin.user.ref", username))
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
				span.SetStatus(codes.Error, "invalid ip address format")
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid IP address format"})
				return
			}
		}

		if ipAddress != "" {
			if isPrivateOrLocalIP(ipAddress) {
				span.SetStatus(codes.Error, "internal ip ban rejected")
				c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot ban internal/local IP address"})
				return
			}
		}

		if req.ExpiryDate != "" {
			parsedExpiry, err := time.Parse(time.RFC3339, req.ExpiryDate)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, "invalid expiry date")
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid expiry_date"})
				return
			}

			if !parsedExpiry.After(time.Now()) {
				span.SetStatus(codes.Error, "expiry must be in future")
				c.JSON(http.StatusBadRequest, gin.H{"error": "expiry_date must be in the future"})
				return
			}

			if time.Until(parsedExpiry) > 365*24*time.Hour {
				span.SetStatus(codes.Error, "expiry too large")
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
			span.SetAttributes(attribute.Bool("ban.already_present", true))
			observability.RecordBusinessEvent(c.Request.Context(), "ban.already_present")
			c.JSON(http.StatusOK, gin.H{
				"status": "already_banned",
				"ban_id": existingBan.ID,
			})
			return
		}

		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to create ban")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create ban"})
			return
		}
		banTarget := "ip"
		if sessionID != "" {
			banTarget = "session"
		}
		observability.RecordBusinessEvent(
			c.Request.Context(),
			"ban.created",
			attribute.String("ban.target", banTarget),
		)

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
		filter, args := reportAutoApprovalFilter(req.ReportID, sessionID, ipAddress)
		reportUpdate := db.GetDB().WithContext(ctx).Model(&storage.Report{}).
			Where("status = ?", "pending").
			Where(filter, args...)

		if err := reportUpdate.Updates(map[string]interface{}{
			"status":               "approved",
			"reviewed_by_username": username,
			"reviewed_at":          reviewedAt,
		}).Error; err != nil {
			log.Printf("Failed to auto-approve related reports after ban: %v", err)
		}

		if redisPropagationFailed {
			span.SetStatus(codes.Error, "redis propagation failed")
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
	searchQuery := strings.ToLower(strings.TrimSpace(c.Query("q")))
	if searchQuery == "" {
		searchQuery = strings.ToLower(strings.TrimSpace(c.Query("ip")))
	}

	if len(searchQuery) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "search query too long"})
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

	if searchQuery != "" {
		likeQuery := "%" + searchQuery + "%"
		query = query.Where(
			"LOWER(ip_address) LIKE ? OR LOWER(session_id) LIKE ? OR LOWER(reason) LIKE ? OR LOWER(banned_by_username) LIKE ?",
			likeQuery,
			likeQuery,
			likeQuery,
			likeQuery,
		)
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
		span := startHandlerSpan(c, "moderation.ban.delete")
		defer span.End()

		banIdentifier := c.Param("session_id")
		span.SetAttributes(hashedAttribute("ban.ref", banIdentifier))

		// Validate that banIdentifier is a valid UUID to prevent injection
		if !uuidRe.MatchString(banIdentifier) {
			span.SetStatus(codes.Error, "invalid ban identifier format")
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ban identifier format"})
			return
		}

		db := storage.NewDatabase()

		username, ok := getContextString(c, "username")
		if !ok {
			span.SetStatus(codes.Error, "unauthorized")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		span.SetAttributes(hashedAttribute("admin.user.ref", username))
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
			span.SetStatus(codes.Error, "ban not found")
			c.JSON(http.StatusNotFound, gin.H{"error": "Ban not found"})
			return
		}

		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to unban")
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
			span.SetStatus(codes.Error, "redis propagation failed")
			c.JSON(http.StatusBadGateway, gin.H{
				"error": "Unban saved, but Redis propagation failed",
			})
			return
		}
		banTarget := "ip"
		if ban.SessionID != "" {
			banTarget = "session"
		}
		observability.RecordBusinessEvent(
			c.Request.Context(),
			"ban.deleted",
			attribute.String("ban.target", banTarget),
		)

		c.JSON(http.StatusOK, gin.H{
			"status": "unbanned",
			"ban_id": ban.ID,
		})
	}
}

func GetBannedWordsHandlerGin(c *gin.Context) {
	db := storage.NewDatabase()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	searchQuery := strings.ToLower(strings.TrimSpace(c.Query("q")))
	if len(searchQuery) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "search query too long"})
		return
	}

	limit := 25
	if limitStr := strings.TrimSpace(c.Query("limit")); limitStr != "" {
		if limitStr == "all" {
			limit = 1000
		} else if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
			if parsed > 1000 {
				limit = 1000
			} else {
				limit = parsed
			}
		}
	}

	query := db.GetDB().WithContext(ctx).Model(&storage.BannedWord{})
	if searchQuery != "" {
		likeQuery := "%" + searchQuery + "%"
		query = query.Where(
			"LOWER(word) LIKE ? OR LOWER(normalized_word) LIKE ? OR LOWER(created_by_username) LIKE ?",
			likeQuery,
			likeQuery,
			likeQuery,
		)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to count banned words"})
		return
	}

	var words []storage.BannedWord
	if err := query.
		Order("normalized_word ASC").
		Limit(limit).
		Find(&words).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch banned words"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"words": words,
		"total": total,
	})
}

func CreateBannedWordHandlerGin(redisClient *appredis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Word string `json:"word" binding:"required,max=128"`
		}

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request properties"})
			return
		}

		word := strings.TrimSpace(stripHTML(req.Word))
		normalizedWord := normalizeBannedWord(word)
		if normalizedWord == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Banned word cannot be empty"})
			return
		}

		username, ok := getContextString(c, "username")
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}

		db := storage.NewDatabase()
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()

		entry := storage.BannedWord{
			Word:              word,
			NormalizedWord:    normalizedWord,
			CreatedByUsername: username,
		}

		err := db.GetDB().WithContext(ctx).Create(&entry).Error
		if err != nil {
			if isDuplicateKeyError(err) {
				var existing storage.BannedWord
				if lookupErr := db.GetDB().WithContext(ctx).
					Where("normalized_word = ?", normalizedWord).
					First(&existing).Error; lookupErr == nil {
					if redisErr := redisClient.GetClient().SAdd(ctx, appredis.BannedWordsSetKey(), normalizedWord).Err(); redisErr != nil {
						log.Printf("Failed to self-heal banned word in Redis: %v", redisErr)
					}
					if publishErr := redisClient.PublishRefreshBannedWordsAction(ctx); publishErr != nil {
						log.Printf("Failed to publish banned words refresh action: %v", publishErr)
					}

					c.JSON(http.StatusOK, gin.H{
						"status": "exists",
						"word":   existing,
					})
					return
				}
			}

			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create banned word"})
			return
		}

		if err := redisClient.GetClient().SAdd(ctx, appredis.BannedWordsSetKey(), normalizedWord).Err(); err != nil {
			log.Printf("Failed to sync banned word to Redis: %v", err)
			if rollbackErr := db.GetDB().WithContext(ctx).Delete(&storage.BannedWord{}, "id = ?", entry.ID).Error; rollbackErr != nil {
				log.Printf("Failed to rollback banned word after Redis error: %v", rollbackErr)
			}
			c.JSON(http.StatusBadGateway, gin.H{"error": "Word saved, but Redis propagation failed"})
			return
		}

		if err := redisClient.PublishRefreshBannedWordsAction(ctx); err != nil {
			log.Printf("Failed to publish banned words refresh action: %v", err)
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "created",
			"word":   entry,
		})
	}
}

func DeleteBannedWordHandlerGin(redisClient *appredis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		wordID := strings.TrimSpace(c.Param("id"))
		if !uuidRe.MatchString(wordID) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid banned word identifier format"})
			return
		}

		db := storage.NewDatabase()
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()

		var entry storage.BannedWord
		if err := db.GetDB().WithContext(ctx).Where("id = ?", wordID).First(&entry).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "Banned word not found"})
				return
			}

			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete banned word"})
			return
		}

		if err := db.GetDB().WithContext(ctx).Delete(&storage.BannedWord{}, "id = ?", wordID).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete banned word"})
			return
		}

		if err := redisClient.GetClient().SRem(ctx, appredis.BannedWordsSetKey(), entry.NormalizedWord).Err(); err != nil {
			log.Printf("Failed to remove banned word from Redis: %v", err)
			c.JSON(http.StatusBadGateway, gin.H{"error": "Word deleted, but Redis propagation failed"})
			return
		}

		if err := redisClient.PublishRefreshBannedWordsAction(ctx); err != nil {
			log.Printf("Failed to publish banned words refresh action: %v", err)
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "deleted",
			"id":     wordID,
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
	ctx, span := startChildSpanFromContext(ctx, "moderation.verify_session_token", hashedAttribute("session.ref", sessionID))
	defer span.End()

	route, err := appredis.ResolveSessionRouteForReport(ctx, redisClient, sessionID)
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.String("session.route.lookup", "report"))
		return false
	}

	expectedToken, err := redisClient.Get(ctx, appredis.SessionTokenKey(sessionID, route)).Result()
	if err != nil || expectedToken == "" {
		if err != nil {
			span.RecordError(err)
		}
		return false
	}

	hash := sha256.Sum256([]byte(providedToken))
	providedHashHex := hex.EncodeToString(hash[:])

	return subtle.ConstantTimeCompare([]byte(expectedToken), []byte(providedHashHex)) == 1
}

func sessionCanReportPeer(ctx context.Context, redisClient redis.UniversalClient, reporterSessionID, reportedSessionID string) bool {
	ctx, span := startChildSpanFromContext(
		ctx,
		"moderation.report_peer_check",
		hashedAttribute("reporter.session.ref", reporterSessionID),
		hashedAttribute("reported.session.ref", reportedSessionID),
	)
	defer span.End()

	route, err := appredis.ResolveSessionRouteForReport(ctx, redisClient, reporterSessionID)
	if err != nil {
		span.RecordError(err)
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

		if err != nil && !errors.Is(err, redis.Nil) {
			span.RecordError(err)
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

func normalizeBannedWord(word string) string {
	parts := strings.Fields(strings.ToLower(word))
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimFunc(part, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r)
		})
		if trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	return strings.Join(normalized, " ")
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
	ctx, span := startChildSpanFromContext(ctx, "moderation.lookup_session_ip", hashedAttribute("session.ref", sessionID))
	defer span.End()

	route, err := appredis.ResolveSessionRoute(ctx, redisClient, sessionID)
	if err != nil {
		span.RecordError(err)
		return ""
	}

	raw, err := redisClient.Get(ctx, appredis.SessionIPKey(sessionID, route)).Result()
	if err != nil {
		span.RecordError(err)
		return ""
	}

	return normalizeIP(raw)
}

func resolveBanIPAddress(ctx context.Context, redisClient redis.UniversalClient, sessionID, requestedIP string) string {
	ctx, span := startChildSpanFromContext(
		ctx,
		"moderation.resolve_ban_ip",
		hashedAttribute("session.ref", sessionID),
	)
	defer span.End()

	if redisClient != nil {
		if ipAddress := lookupSessionIP(ctx, redisClient, sessionID); ipAddress != "" {
			span.SetAttributes(attribute.String("ban.ip_source", "session_lookup"))
			return ipAddress
		}
	}

	span.SetAttributes(attribute.String("ban.ip_source", "request"))
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

func reportAutoApprovalFilter(reportID, sessionID, ipAddress string) (string, []interface{}) {
	switch {
	case reportID != "" && sessionID != "" && ipAddress != "":
		return "id = ? AND (reported_session_id = ? OR reported_ip = ?)", []interface{}{reportID, sessionID, ipAddress}
	case reportID != "" && sessionID != "":
		return "id = ? AND reported_session_id = ?", []interface{}{reportID, sessionID}
	case reportID != "" && ipAddress != "":
		return "id = ? AND reported_ip = ?", []interface{}{reportID, ipAddress}
	case reportID != "":
		return "id = ?", []interface{}{reportID}
	case sessionID != "" && ipAddress != "":
		return "(reported_session_id = ? OR reported_ip = ?)", []interface{}{sessionID, ipAddress}
	case sessionID != "":
		return "reported_session_id = ?", []interface{}{sessionID}
	default:
		return "reported_ip = ?", []interface{}{ipAddress}
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
	ctx, span := startChildSpanFromContext(ctx, "admin.report_metrics.load")
	defer span.End()

	rows := make([]reportMetricRow, 0, 3)
	if err := db.WithContext(ctx).
		Model(&storage.Report{}).
		Select("status, COUNT(*) AS count").
		Where("status IN ?", []string{"pending", "approved", "rejected"}).
		Group("status").
		Scan(&rows).Error; err != nil {
		span.RecordError(err)
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
	ctx, span := startChildSpanFromContext(ctx, "admin.ban_metrics.load")
	defer span.End()

	rows := make([]banMetricRow, 0, 2)
	if err := db.WithContext(ctx).
		Model(&storage.Ban{}).
		Select("is_active, COUNT(*) AS count").
		Group("is_active").
		Scan(&rows).Error; err != nil {
		span.RecordError(err)
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
