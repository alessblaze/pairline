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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anish/omegle/backend/golang/internal/middleware"
	"github.com/anish/omegle/backend/golang/internal/redis"
	"github.com/anish/omegle/backend/golang/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const adminCSRFCookieName = "admin_csrf_token"

func HealthHandlerGin(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "ok",
		"service":   "omegle-go-service",
		"timestamp": time.Now().UnixMilli(),
	})
}

func LoginHandlerGin(c *gin.Context) {
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
	jwtExpHours := getEnvAsInt("JWT_EXPIRATION_HOURS", "8")

	token, err := middleware.GenerateJWT(admin.Username, admin.Role, jwtSecret, jwtExpHours)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate session token"})
		return
	}

	isSecure := c.Request.TLS != nil || c.Request.Header.Get("X-Forwarded-Proto") == "https"
	if isSecure {
		c.SetSameSite(http.SameSiteNoneMode)
	} else {
		c.SetSameSite(http.SameSiteLaxMode)
	}
	csrfToken := generateOpaqueToken()
	c.SetCookie("admin_token", token, jwtExpHours*3600, "/", "", isSecure, true)
	c.SetCookie(adminCSRFCookieName, csrfToken, jwtExpHours*3600, "/", "", isSecure, false)

	c.JSON(http.StatusOK, gin.H{
		"role":       admin.Role,
		"csrf_token": csrfToken,
	})
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
				limit = parsed
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

	var metricPending int64
	var metricApproved int64
	var metricRejected int64

	db.GetDB().WithContext(ctx).Model(&storage.Report{}).Where("status = ?", "pending").Count(&metricPending)
	db.GetDB().WithContext(ctx).Model(&storage.Report{}).Where("status = ?", "approved").Count(&metricApproved)
	db.GetDB().WithContext(ctx).Model(&storage.Report{}).Where("status = ?", "rejected").Count(&metricRejected)

	c.JSON(http.StatusOK, gin.H{
		"reports": reports,
		"metrics": map[string]int64{
			"pending":  metricPending,
			"approved": metricApproved,
			"rejected": metricRejected,
		},
	})
}

func UpdateReportHandlerGin(c *gin.Context) {
	id := c.Param("id")

	// Validate UUID format for report ID
	if !uuidRe.MatchString(id) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid report ID format"})
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
	tx := db.GetDB().WithContext(ctx).Model(&storage.Report{}).Where("id = ?", id).Update("status", req.Status)

	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update report"})
		return
	}

	if tx.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Report not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "updated",
	})
}

func CreateReportHandlerGin(c *gin.Context) {
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

	db := storage.NewDatabase()
	redisClient := redis.NewClient()
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

	reporterIP, _ := redisClient.GetClient().Get(ctx, "session:"+req.ReporterSessionID+":ip").Result()
	reportedIP, _ := redisClient.GetClient().Get(ctx, "session:"+req.ReportedSessionID+":ip").Result()

	if reporterIP == "" {
		reporterIP = getRequestClientIP(c)
	}

	if reporterIP != "" && net.ParseIP(reporterIP) == nil {
		reporterIP = ""
	}
	if reportedIP != "" && net.ParseIP(reportedIP) == nil {
		reportedIP = ""
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

func CreateBanHandlerGin(c *gin.Context) {
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

	db := storage.NewDatabase()
	redisClient := redis.NewClient()

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
		ipAddress, _ = redisClient.GetClient().Get(ctx, "session:"+req.SessionID+":ip").Result()
	} else {
		ipAddress = req.IP
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

	var existingBan storage.Ban
	lookup := db.GetDB().WithContext(ctx).Where("is_active = ? AND (expires_at IS NULL OR expires_at > ?)", true, time.Now())

	switch {
	case sessionID != "" && ipAddress != "":
		lookup = lookup.Where("(session_id = ? OR ip_address = ?)", sessionID, ipAddress)
	case sessionID != "":
		lookup = lookup.Where("session_id = ?", sessionID)
	default:
		lookup = lookup.Where("ip_address = ?", ipAddress)
	}

	if err := lookup.Order("created_at DESC").First(&existingBan).Error; err == nil {
		c.JSON(http.StatusOK, gin.H{
			"status": "already_banned",
			"ban_id": existingBan.ID,
		})
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing bans"})
		return
	}

	ban := storage.Ban{
		SessionID:        sessionID,
		IPAddress:        ipAddress,
		Reason:           req.Reason,
		BannedByUsername: username,
		CreatedAt:        time.Now(),
		ExpiresAt:        expiresAt,
		IsActive:         true,
	}

	result := db.GetDB().WithContext(ctx).Create(&ban).Error
	if result != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create ban"})
		return
	}

	redisTTL := time.Duration(0)
	if expiresAt != nil {
		redisTTL = time.Until(*expiresAt)
	}

	if sessionID != "" {
		err := redisClient.GetClient().Set(ctx, "ban:"+sessionID, req.Reason, redisTTL).Err()
		if err != nil {
			log.Printf("Failed to store ban in Redis: %v", err)
		}

		err = redisClient.PublishBanAction(ctx, sessionID, ipAddress, req.Reason)
		if err != nil {
			log.Printf("Failed to publish ban action: %v", err)
		}
	}

	if ipAddress != "" {
		err := redisClient.GetClient().Set(ctx, "ban:ip:"+ipAddress, req.Reason, redisTTL).Err()
		if err != nil {
			log.Printf("Failed to store IP ban in Redis: %v", err)
		}

		err = redisClient.PublishBanIPAction(ctx, ipAddress, req.Reason)
		if err != nil {
			log.Printf("Failed to publish ban IP action: %v", err)
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

	c.JSON(http.StatusOK, gin.H{
		"status": "banned",
		"ban_id": ban.ID,
	})
}

func GetBansHandlerGin(c *gin.Context) {
	status := c.Query("status")
	limitStr := c.Query("limit")

	limit := 10
	if limitStr != "" {
		if limitStr == "all" {
			limit = 1000
		} else {
			if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
				limit = parsed
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

	var bans []storage.Ban
	result := query.Order("created_at DESC").Limit(limit).Find(&bans).Error

	if result != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch bans"})
		return
	}

	var activeCount int64
	var inactiveCount int64

	db.GetDB().WithContext(ctx).Model(&storage.Ban{}).Where("is_active = ?", true).Count(&activeCount)
	db.GetDB().WithContext(ctx).Model(&storage.Ban{}).Where("is_active = ?", false).Count(&inactiveCount)

	c.JSON(http.StatusOK, gin.H{
		"bans": bans,
		"metrics": map[string]int64{
			"active":   activeCount,
			"inactive": inactiveCount,
			"total":    activeCount + inactiveCount,
		},
	})
}

func DeleteBanHandlerGin(c *gin.Context) {
	banIdentifier := c.Param("session_id")

	// Validate that banIdentifier is a valid UUID to prevent injection
	if !uuidRe.MatchString(banIdentifier) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ban identifier format"})
		return
	}

	db := storage.NewDatabase()
	redisClient := redis.NewClient()

	username, ok := getContextString(c, "username")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	now := time.Now()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	var ban storage.Ban
	result := db.GetDB().WithContext(ctx).
		Where("id = ? AND is_active = ?", banIdentifier, true).
		First(&ban).Error

	if errors.Is(result, gorm.ErrRecordNotFound) {
		result = db.GetDB().WithContext(ctx).
			Where("session_id = ? AND is_active = ?", banIdentifier, true).
			Order("created_at DESC").
			First(&ban).Error
	}

	if result != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Ban not found"})
		return
	}

	result = db.GetDB().WithContext(ctx).Model(&storage.Ban{}).
		Where("id = ?", ban.ID).
		Updates(map[string]interface{}{
			"is_active":            false,
			"unbanned_at":          now,
			"unbanned_by_username": username,
		}).Error

	if result != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to unban"})
		return
	}

	if ban.SessionID != "" {
		err := redisClient.GetClient().Del(ctx, "ban:"+ban.SessionID).Err()
		if err != nil {
			log.Printf("Failed to delete ban from Redis: %v", err)
		}
	}

	if ban.IPAddress != "" {
		err := redisClient.GetClient().Del(ctx, "ban:ip:"+ban.IPAddress).Err()
		if err != nil {
			log.Printf("Failed to delete IP ban from Redis: %v", err)
		}
	}

	if ban.SessionID != "" {
		err := redisClient.PublishUnbanAction(ctx, ban.SessionID, ban.IPAddress)
		if err != nil {
			log.Printf("Failed to publish unban action: %v", err)
		}
	}

	if ban.IPAddress != "" {
		err := redisClient.PublishUnbanIPAction(ctx, ban.IPAddress)
		if err != nil {
			log.Printf("Failed to publish unban IP action: %v", err)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "unbanned",
		"ban_id": ban.ID,
	})
}

func CreateAdminHandlerGin(c *gin.Context) {
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

	db := storage.NewDatabase()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	var existingAdmin storage.AdminAccount
	result := db.GetDB().WithContext(ctx).Where("username = ?", req.Username).First(&existingAdmin).Error
	if result == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Admin already exists"})
		return
	}

	hash, err := storage.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to securely hash password"})
		return
	}

	admin := storage.AdminAccount{
		Username:     req.Username,
		PasswordHash: hash,
		Role:         req.Role,
		IsActive:     true,
		CreatedAt:    time.Now(),
	}

	result = db.GetDB().WithContext(ctx).Create(&admin).Error
	if result != nil {
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

func getContextString(c *gin.Context, key string) (string, bool) {
	value, exists := c.Get(key)
	if !exists {
		return "", false
	}

	str, ok := value.(string)
	return str, ok && str != ""
}

func verifySessionToken(ctx context.Context, redisClient *redis.Client, sessionID, providedToken string) bool {
	expectedToken, err := redisClient.GetClient().Get(ctx, "session:"+sessionID+":token").Result()
	if err != nil || expectedToken == "" {
		return false
	}

	hash := sha256.Sum256([]byte(providedToken))
	providedHashHex := hex.EncodeToString(hash[:])

	return subtle.ConstantTimeCompare([]byte(expectedToken), []byte(providedHashHex)) == 1
}

func sessionCanReportPeer(ctx context.Context, redisClient *redis.Client, reporterSessionID, reportedSessionID string) bool {
	keys := []string{
		"match:" + reporterSessionID,
		"recent_match:" + reporterSessionID,
	}

	for _, key := range keys {
		peerID, err := redisClient.GetClient().Get(ctx, key).Result()
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
	trustedPrefixesOnce sync.Once
	cachedTrustedPrefixes []netip.Prefix
)

func trustedProxyPrefixes() []netip.Prefix {
	trustedPrefixesOnce.Do(func() {
		raw := os.Getenv("TRUSTED_PROXY_CIDRS")
		if raw == "" {
			raw = "127.0.0.1/32"
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
