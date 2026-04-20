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
package bots

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/anish/omegle/backend/golang/internal/storage"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	SettingEnabledKey            = "bots.enabled"
	SettingEngagementEnabledKey  = "bots.engagement.enabled"
	SettingAIEnabledKey          = "bots.ai.enabled"
	SettingRolloutPercentKey     = "bots.match.rollout_percent"
	SettingMaxConcurrentKey      = "bots.max_concurrent_runs"
	SettingEngagementPriorityKey = "bots.engagement.priority"
	SettingAIPriorityKey         = "bots.ai.priority"
	SettingEmergencyStopKey      = "bots.emergency_stop"

	defaultRolloutPercent     = 0
	defaultMaxConcurrentRuns  = 100
	defaultEngagementPriority = 100
	defaultAIPriority         = 100
)

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,62}$`)

type SettingsResponse struct {
	Enabled             bool `json:"enabled"`
	EngagementEnabled   bool `json:"engagement_enabled"`
	AIEnabled           bool `json:"ai_enabled"`
	RolloutPercent      int  `json:"rollout_percent"`
	MaxConcurrentRuns   int  `json:"max_concurrent_runs"`
	EngagementPriority  int  `json:"engagement_priority"`
	AIPriority          int  `json:"ai_priority"`
	EmergencyStop       bool `json:"emergency_stop"`
	EnabledConfigured   bool `json:"enabled_configured"`
	AIConfigured        bool `json:"ai_configured"`
	EmergencyConfigured bool `json:"emergency_stop_configured"`
}

func GetSettingsHandler(c *gin.Context) {
	span := startSpan(c, "bots.settings.get")
	defer span.End()

	db := storage.NewDatabase()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := LoadSettings(ctx, db.GetDB())
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to load bot settings")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load bot settings"})
		return
	}

	c.JSON(http.StatusOK, resp)
}

func UpdateSettingsHandler(c *gin.Context, sync SnapshotSyncer) {
	span := startSpan(c, "bots.settings.update")
	defer span.End()

	var req struct {
		Enabled            *bool `json:"enabled"`
		EngagementEnabled  *bool `json:"engagement_enabled"`
		AIEnabled          *bool `json:"ai_enabled"`
		RolloutPercent     *int  `json:"rollout_percent"`
		MaxConcurrentRuns  *int  `json:"max_concurrent_runs"`
		EngagementPriority *int  `json:"engagement_priority"`
		AIPriority         *int  `json:"ai_priority"`
		EmergencyStop      *bool `json:"emergency_stop"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		span.SetStatus(codes.Error, "invalid request")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request properties"})
		return
	}

	if req.Enabled == nil &&
		req.EngagementEnabled == nil &&
		req.AIEnabled == nil &&
		req.RolloutPercent == nil &&
		req.MaxConcurrentRuns == nil &&
		req.EngagementPriority == nil &&
		req.AIPriority == nil &&
		req.EmergencyStop == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No bot settings were provided"})
		return
	}

	if req.RolloutPercent != nil && (*req.RolloutPercent < 0 || *req.RolloutPercent > 100) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "rollout_percent must be between 0 and 100"})
		return
	}

	if req.MaxConcurrentRuns != nil && (*req.MaxConcurrentRuns < 1 || *req.MaxConcurrentRuns > 100000) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "max_concurrent_runs must be between 1 and 100000"})
		return
	}

	if req.EngagementPriority != nil && (*req.EngagementPriority < 0 || *req.EngagementPriority > 100000) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "engagement_priority must be between 0 and 100000"})
		return
	}

	if req.AIPriority != nil && (*req.AIPriority < 0 || *req.AIPriority > 100000) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ai_priority must be between 0 and 100000"})
		return
	}

	username, ok := getContextString(c, "username")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	span.SetAttributes(hashedAttribute("admin.user.ref", username))

	db := storage.NewDatabase()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	updates := make([]storage.AdminSetting, 0, 8)
	if req.Enabled != nil {
		updates = append(updates, storage.AdminSetting{
			Key:               SettingEnabledKey,
			Value:             boolSettingValue(*req.Enabled),
			UpdatedByUsername: username,
		})
	}
	if req.EngagementEnabled != nil {
		updates = append(updates, storage.AdminSetting{
			Key:               SettingEngagementEnabledKey,
			Value:             boolSettingValue(*req.EngagementEnabled),
			UpdatedByUsername: username,
		})
	}
	if req.AIEnabled != nil {
		updates = append(updates, storage.AdminSetting{
			Key:               SettingAIEnabledKey,
			Value:             boolSettingValue(*req.AIEnabled),
			UpdatedByUsername: username,
		})
	}
	if req.RolloutPercent != nil {
		updates = append(updates, storage.AdminSetting{
			Key:               SettingRolloutPercentKey,
			Value:             strconv.Itoa(*req.RolloutPercent),
			UpdatedByUsername: username,
		})
	}
	if req.MaxConcurrentRuns != nil {
		updates = append(updates, storage.AdminSetting{
			Key:               SettingMaxConcurrentKey,
			Value:             strconv.Itoa(*req.MaxConcurrentRuns),
			UpdatedByUsername: username,
		})
	}
	if req.EngagementPriority != nil {
		updates = append(updates, storage.AdminSetting{
			Key:               SettingEngagementPriorityKey,
			Value:             strconv.Itoa(*req.EngagementPriority),
			UpdatedByUsername: username,
		})
	}
	if req.AIPriority != nil {
		updates = append(updates, storage.AdminSetting{
			Key:               SettingAIPriorityKey,
			Value:             strconv.Itoa(*req.AIPriority),
			UpdatedByUsername: username,
		})
	}
	if req.EmergencyStop != nil {
		updates = append(updates, storage.AdminSetting{
			Key:               SettingEmergencyStopKey,
			Value:             boolSettingValue(*req.EmergencyStop),
			UpdatedByUsername: username,
		})
	}

	if err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, setting := range updates {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "key"}},
				DoUpdates: clause.AssignmentColumns([]string{"value", "updated_by_username", "updated_at"}),
			}).Create(&setting).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to update bot settings")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update bot settings"})
		return
	}

	if sync != nil {
		if err := sync.Sync(ctx, db.GetDB(), username); err != nil {
			span.RecordError(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to publish bot snapshot"})
			return
		}
	}

	resp, err := LoadSettings(ctx, db.GetDB())
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to reload bot settings")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load bot settings"})
		return
	}

	c.JSON(http.StatusOK, resp)
}

func ListDefinitionsHandler(c *gin.Context) {
	span := startSpan(c, "bots.definitions.list")
	defer span.End()

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

	searchQuery := strings.TrimSpace(c.Query("q"))
	if len(searchQuery) > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "search query too long"})
		return
	}

	botType := strings.TrimSpace(strings.ToLower(c.Query("bot_type")))
	if botType != "" && !isValidBotType(botType) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid bot_type"})
		return
	}

	active := strings.TrimSpace(strings.ToLower(c.Query("active")))

	db := storage.NewDatabase()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	role, _ := getContextString(c, "role")

	query := db.GetDB().WithContext(ctx).Model(&Definition{})
	if searchQuery != "" {
		like := "%" + strings.ToLower(searchQuery) + "%"
		query = query.Where("LOWER(name) LIKE ? OR LOWER(slug) LIKE ?", like, like)
	}
	if botType != "" {
		query = query.Where("bot_type = ?", botType)
	}
	switch active {
	case "true":
		query = query.Where("is_active = ?", true)
	case "false":
		query = query.Where("is_active = ?", false)
	}

	var definitions []Definition
	if err := query.Order("updated_at DESC").Limit(limit).Find(&definitions).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch bot definitions"})
		return
	}

	for idx := range definitions {
		definitions[idx] = sanitizeDefinitionForRole(definitions[idx], role)
	}

	c.JSON(http.StatusOK, gin.H{"bots": definitions})
}

func GetDefinitionHandler(c *gin.Context) {
	span := startSpan(c, "bots.definitions.get")
	defer span.End()

	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Bot id is required"})
		return
	}

	db := storage.NewDatabase()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	role, _ := getContextString(c, "role")

	var definition Definition
	if err := db.GetDB().WithContext(ctx).Where("id = ?", id).First(&definition).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Bot definition not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch bot definition"})
		return
	}

	c.JSON(http.StatusOK, sanitizeDefinitionForRole(definition, role))
}

func CreateDefinitionHandler(c *gin.Context, sync SnapshotSyncer) {
	span := startSpan(c, "bots.definitions.create")
	defer span.End()

	var req struct {
		Name              string           `json:"name"`
		Slug              string           `json:"slug"`
		BotType           string           `json:"bot_type"`
		IsActive          *bool            `json:"is_active"`
		Description       string           `json:"description"`
		MatchModes        []string         `json:"match_modes"`
		BotCount          *int             `json:"bot_count"`
		TrafficWeight     *int             `json:"traffic_weight"`
		TargetingJSON     *json.RawMessage `json:"targeting_json"`
		ScriptJSON        *json.RawMessage `json:"script_json"`
		AIConfigJSON      *json.RawMessage `json:"ai_config_json"`
		MessageLimit      *int             `json:"message_limit"`
		SessionTTLSeconds *int             `json:"session_ttl_seconds"`
		IdleTimeoutSecs   *int             `json:"idle_timeout_seconds"`
		DisconnectReason  string           `json:"disconnect_reason"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request properties"})
		return
	}

	if err := validateDefinitionRequest(req.Name, req.Slug, req.BotType); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	normalizedAIConfig, err := normalizeDefinitionAIConfig(req.BotType, req.AIConfigJSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	username, ok := getContextString(c, "username")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	span.SetAttributes(hashedAttribute("admin.user.ref", username))

	definition := Definition{
		Name:              strings.TrimSpace(req.Name),
		Slug:              normalizeSlug(req.Slug),
		BotType:           strings.ToLower(strings.TrimSpace(req.BotType)),
		IsActive:          true,
		Description:       strings.TrimSpace(req.Description),
		MatchModesJSON:    mustMarshalJSON(defaultMatchModes(req.MatchModes)),
		BotCount:          1,
		TrafficWeight:     100,
		TargetingJSON:     defaultJSONObject(req.TargetingJSON),
		ScriptJSON:        defaultJSONObject(req.ScriptJSON),
		AIConfigJSON:      normalizedAIConfig,
		MessageLimit:      20,
		SessionTTLSeconds: 300,
		IdleTimeoutSecs:   45,
		DisconnectReason:  strings.TrimSpace(req.DisconnectReason),
		CreatedByUsername: username,
		UpdatedByUsername: username,
	}

	if req.IsActive != nil {
		definition.IsActive = *req.IsActive
	}
	if req.BotCount != nil {
		if err := validateBotCount(*req.BotCount); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		definition.BotCount = *req.BotCount
	}
	if req.TrafficWeight != nil {
		if *req.TrafficWeight < 1 || *req.TrafficWeight > 100000 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "traffic_weight must be between 1 and 100000"})
			return
		}
		definition.TrafficWeight = *req.TrafficWeight
	}
	if req.MessageLimit != nil {
		if *req.MessageLimit < 1 || *req.MessageLimit > 200 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "message_limit must be between 1 and 200"})
			return
		}
		definition.MessageLimit = *req.MessageLimit
	}
	if req.SessionTTLSeconds != nil {
		if *req.SessionTTLSeconds < 5 || *req.SessionTTLSeconds > 86400 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "session_ttl_seconds must be between 5 and 86400"})
			return
		}
		definition.SessionTTLSeconds = *req.SessionTTLSeconds
	}
	if req.IdleTimeoutSecs != nil {
		if *req.IdleTimeoutSecs < 5 || *req.IdleTimeoutSecs > 3600 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "idle_timeout_seconds must be between 5 and 3600"})
			return
		}
		definition.IdleTimeoutSecs = *req.IdleTimeoutSecs
	}

	db := storage.NewDatabase()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	if err := db.GetDB().WithContext(ctx).Create(&definition).Error; err != nil {
		if isDuplicateKeyError(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "Bot definition with this slug already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create bot definition"})
		return
	}

	if sync != nil {
		if err := sync.Sync(ctx, db.GetDB(), username); err != nil {
			span.RecordError(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to publish bot snapshot"})
			return
		}
	}

	c.JSON(http.StatusOK, definition)
}

func UpdateDefinitionHandler(c *gin.Context, sync SnapshotSyncer) {
	span := startSpan(c, "bots.definitions.update")
	defer span.End()

	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Bot id is required"})
		return
	}

	var req struct {
		Name              *string          `json:"name"`
		Slug              *string          `json:"slug"`
		IsActive          *bool            `json:"is_active"`
		Description       *string          `json:"description"`
		MatchModes        []string         `json:"match_modes"`
		BotCount          *int             `json:"bot_count"`
		TrafficWeight     *int             `json:"traffic_weight"`
		TargetingJSON     *json.RawMessage `json:"targeting_json"`
		ScriptJSON        *json.RawMessage `json:"script_json"`
		AIConfigJSON      *json.RawMessage `json:"ai_config_json"`
		MessageLimit      *int             `json:"message_limit"`
		SessionTTLSeconds *int             `json:"session_ttl_seconds"`
		IdleTimeoutSecs   *int             `json:"idle_timeout_seconds"`
		DisconnectReason  *string          `json:"disconnect_reason"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request properties"})
		return
	}

	username, ok := getContextString(c, "username")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	span.SetAttributes(hashedAttribute("admin.user.ref", username))

	db := storage.NewDatabase()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	var definition Definition
	if err := db.GetDB().WithContext(ctx).Where("id = ?", id).First(&definition).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Bot definition not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load bot definition"})
		return
	}

	updates := map[string]any{
		"updated_by_username": username,
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len(name) > 120 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name is required and must be at most 120 characters"})
			return
		}
		updates["name"] = name
	}
	if req.Slug != nil {
		slug := normalizeSlug(*req.Slug)
		if !slugRe.MatchString(slug) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "slug must be 3-63 characters using lowercase letters, numbers, hyphens, or underscores"})
			return
		}
		updates["slug"] = slug
	}
	if req.IsActive != nil {
		updates["is_active"] = *req.IsActive
	}
	if req.Description != nil {
		updates["description"] = strings.TrimSpace(*req.Description)
	}
	if req.MatchModes != nil {
		updates["match_modes_json"] = mustMarshalJSON(defaultMatchModes(req.MatchModes))
	}
	if req.BotCount != nil {
		if err := validateBotCount(*req.BotCount); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		updates["bot_count"] = *req.BotCount
	}
	if req.TrafficWeight != nil {
		if *req.TrafficWeight < 1 || *req.TrafficWeight > 100000 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "traffic_weight must be between 1 and 100000"})
			return
		}
		updates["traffic_weight"] = *req.TrafficWeight
	}
	if req.TargetingJSON != nil {
		updates["targeting_json"] = defaultJSONObject(req.TargetingJSON)
	}
	if req.ScriptJSON != nil {
		updates["script_json"] = defaultJSONObject(req.ScriptJSON)
	}
	if req.AIConfigJSON != nil {
		normalizedAIConfig, err := normalizeDefinitionAIConfig(definition.BotType, req.AIConfigJSON)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		updates["ai_config_json"] = normalizedAIConfig
	}
	if req.MessageLimit != nil {
		if *req.MessageLimit < 1 || *req.MessageLimit > 200 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "message_limit must be between 1 and 200"})
			return
		}
		updates["message_limit"] = *req.MessageLimit
	}
	if req.SessionTTLSeconds != nil {
		if *req.SessionTTLSeconds < 5 || *req.SessionTTLSeconds > 86400 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "session_ttl_seconds must be between 5 and 86400"})
			return
		}
		updates["session_ttl_seconds"] = *req.SessionTTLSeconds
	}
	if req.IdleTimeoutSecs != nil {
		if *req.IdleTimeoutSecs < 5 || *req.IdleTimeoutSecs > 3600 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "idle_timeout_seconds must be between 5 and 3600"})
			return
		}
		updates["idle_timeout_seconds"] = *req.IdleTimeoutSecs
	}
	if req.DisconnectReason != nil {
		updates["disconnect_reason"] = strings.TrimSpace(*req.DisconnectReason)
	}

	if len(updates) == 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No updates were provided"})
		return
	}

	if err := db.GetDB().WithContext(ctx).Model(&definition).Updates(updates).Error; err != nil {
		if isDuplicateKeyError(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "Bot definition with this slug already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update bot definition"})
		return
	}

	if sync != nil {
		if err := sync.Sync(ctx, db.GetDB(), username); err != nil {
			span.RecordError(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to publish bot snapshot"})
			return
		}
	}

	if err := db.GetDB().WithContext(ctx).Where("id = ?", id).First(&definition).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reload bot definition"})
		return
	}

	c.JSON(http.StatusOK, definition)
}

func ActivateDefinitionHandler(c *gin.Context, sync SnapshotSyncer) {
	updateDefinitionActiveState(c, true, sync)
}

func DeactivateDefinitionHandler(c *gin.Context, sync SnapshotSyncer) {
	updateDefinitionActiveState(c, false, sync)
}

func DeleteDefinitionHandler(c *gin.Context, sync SnapshotSyncer) {
	span := startSpan(c, "bots.definitions.delete")
	defer span.End()

	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Bot id is required"})
		return
	}

	username, ok := getContextString(c, "username")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	span.SetAttributes(hashedAttribute("admin.user.ref", username))

	db := storage.NewDatabase()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	var definition Definition
	if err := db.GetDB().WithContext(ctx).Where("id = ?", id).First(&definition).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Bot definition not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load bot definition"})
		return
	}

	if err := db.GetDB().WithContext(ctx).Delete(&definition).Error; err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to delete bot definition")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete bot definition"})
		return
	}

	if sync != nil {
		if err := sync.Sync(ctx, db.GetDB(), username); err != nil {
			span.RecordError(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to publish bot snapshot"})
			return
		}
	}

	c.JSON(http.StatusOK, definition)
}

func startSpan(c *gin.Context, name string) trace.Span {
	ctx, span := otel.Tracer("pairline-go-admin").Start(c.Request.Context(), name)
	c.Request = c.Request.WithContext(ctx)
	return span
}

func hashedAttribute(key, value string) attribute.KeyValue {
	return attribute.String(key, value)
}

func getContextString(c *gin.Context, key string) (string, bool) {
	value, exists := c.Get(key)
	if !exists {
		return "", false
	}

	str, ok := value.(string)
	return str, ok && str != ""
}

func boolSettingValue(enabled bool) string {
	if enabled {
		return "true"
	}
	return "false"
}

func parseBoolSetting(value string, defaultValue bool) bool {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "":
		return defaultValue
	case "0", "false", "off", "no", "disabled":
		return false
	default:
		return true
	}
}

func LoadSettings(ctx context.Context, db *gorm.DB) (SettingsResponse, error) {
	values, err := getAdminSettingsMap(ctx, db,
		SettingEnabledKey,
		SettingEngagementEnabledKey,
		SettingAIEnabledKey,
		SettingRolloutPercentKey,
		SettingMaxConcurrentKey,
		SettingEngagementPriorityKey,
		SettingAIPriorityKey,
		SettingEmergencyStopKey,
	)
	if err != nil {
		return SettingsResponse{}, err
	}

	enabledValue, enabledConfigured := values[SettingEnabledKey]
	aiValue, aiConfigured := values[SettingAIEnabledKey]
	emergencyValue, emergencyConfigured := values[SettingEmergencyStopKey]

	return SettingsResponse{
		Enabled:             parseBoolSetting(enabledValue, true),
		EngagementEnabled:   parseBoolSetting(values[SettingEngagementEnabledKey], true),
		AIEnabled:           parseBoolSetting(aiValue, true),
		RolloutPercent:      parseConfiguredInt(values[SettingRolloutPercentKey], defaultRolloutPercent),
		MaxConcurrentRuns:   parseConfiguredInt(values[SettingMaxConcurrentKey], defaultMaxConcurrentRuns),
		EngagementPriority:  parseConfiguredInt(values[SettingEngagementPriorityKey], defaultEngagementPriority),
		AIPriority:          parseConfiguredInt(values[SettingAIPriorityKey], defaultAIPriority),
		EmergencyStop:       parseBoolSetting(emergencyValue, false),
		EnabledConfigured:   enabledConfigured,
		AIConfigured:        aiConfigured,
		EmergencyConfigured: emergencyConfigured,
	}, nil
}

func getAdminSettingsMap(ctx context.Context, db *gorm.DB, keys ...string) (map[string]string, error) {
	var settings []storage.AdminSetting
	if err := db.WithContext(ctx).Where("key IN ?", keys).Find(&settings).Error; err != nil {
		return nil, err
	}

	values := make(map[string]string, len(settings))
	for _, setting := range settings {
		values[setting.Key] = setting.Value
	}
	return values, nil
}

func parseConfiguredInt(value string, defaultValue int) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}
	return parsed
}

func validateDefinitionRequest(name, slug, botType string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 120 {
		return errors.New("name is required and must be at most 120 characters")
	}
	if !slugRe.MatchString(normalizeSlug(slug)) {
		return errors.New("slug must be 3-63 characters using lowercase letters, numbers, hyphens, or underscores")
	}
	if !isValidBotType(botType) {
		return errors.New("bot_type must be engagement or ai")
	}
	return nil
}

func isValidBotType(botType string) bool {
	switch strings.ToLower(strings.TrimSpace(botType)) {
	case "engagement", "ai":
		return true
	default:
		return false
	}
}

func normalizeSlug(slug string) string {
	return strings.ToLower(strings.TrimSpace(slug))
}

func validateBotCount(botCount int) error {
	if botCount < 1 || botCount > 10000 {
		return errors.New("bot_count must be between 1 and 10000")
	}
	return nil
}

func defaultMatchModes(matchModes []string) []string {
	if len(matchModes) == 0 {
		return []string{"text"}
	}

	normalized := make([]string, 0, len(matchModes))
	seen := make(map[string]struct{}, len(matchModes))
	for _, mode := range matchModes {
		trimmed := strings.ToLower(strings.TrimSpace(mode))
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	if len(normalized) == 0 {
		return []string{"text"}
	}
	return normalized
}

func defaultJSONObject(raw *json.RawMessage) json.RawMessage {
	if raw == nil || len(*raw) == 0 || string(*raw) == "null" {
		return mustMarshalJSON(map[string]any{})
	}
	return json.RawMessage(append([]byte(nil), (*raw)...))
}

func mustMarshalJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return json.RawMessage(encoded)
}

func updateDefinitionActiveState(c *gin.Context, active bool, sync SnapshotSyncer) {
	span := startSpan(c, "bots.definitions.state.update")
	defer span.End()

	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Bot id is required"})
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

	tx := db.GetDB().WithContext(ctx).Model(&Definition{}).Where("id = ?", id).Updates(map[string]any{
		"is_active":           active,
		"updated_by_username": username,
	})
	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update bot definition"})
		return
	}
	if tx.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Bot definition not found"})
		return
	}

	if sync != nil {
		if err := sync.Sync(ctx, db.GetDB(), username); err != nil {
			span.RecordError(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to publish bot snapshot"})
			return
		}
	}

	var definition Definition
	if err := db.GetDB().WithContext(ctx).Where("id = ?", id).First(&definition).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reload bot definition"})
		return
	}

	c.JSON(http.StatusOK, definition)
}
