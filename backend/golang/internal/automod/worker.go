package automod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/anish/omegle/backend/golang/internal/automod/models"
	"github.com/anish/omegle/backend/golang/internal/automod/models/shared"
	"github.com/anish/omegle/backend/golang/internal/moderation"
	appredis "github.com/anish/omegle/backend/golang/internal/redis"
	"github.com/anish/omegle/backend/golang/internal/storage"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	EnabledSettingKey = "moderation.auto_reports.enabled"
	ReviewerName      = "auto-moderation"

	defaultAPIBaseURL = "https://integrate.api.nvidia.com/v1"
	defaultModel      = "nvidia/llama-3.1-nemotron-safety-guard-8b-v3"

	statePending    = "pending"
	stateProcessing = "processing"
	stateCompleted  = "completed"
	stateFailed     = "failed"

	decisionApproved = "approved"
	decisionRejected = "rejected"
	decisionEscalate = "escalate"

	autoBanDuration = 7 * 24 * time.Hour
)

type Config struct {
	EnabledDefault bool
	APIKey         string
	BaseURL        string
	Model          string
	ModelType      string
	BatchSize      int
	Interval       time.Duration
	Timeout        time.Duration
	ClaimTimeout   time.Duration
	MaxAttempts    int
	Temperature    float64
	MaxTokens      int64
	DebugLogging   bool
}

type Worker struct {
	db         *gorm.DB
	redis      *appredis.Client
	client     *http.Client
	config     Config
	triggerCh  chan string
	processing chan struct{}
}

type StatusResponse struct {
	Enabled         bool   `json:"enabled"`
	EnabledDefault  bool   `json:"enabled_default"`
	Configured      bool   `json:"configured"`
	Model           string `json:"model"`
	ModelType       string `json:"model_type"`
	BatchSize       int    `json:"batch_size"`
	IntervalSeconds int    `json:"interval_seconds"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	MaxAttempts     int    `json:"max_attempts"`
}

type chatLogMessage struct {
	Text   string `json:"text"`
	Sender string `json:"sender"`
}

type safetyAssessment struct {
	UserSafety     string `json:"User Safety"`
	ResponseSafety string `json:"Response Safety,omitempty"`
	Categories     []string
}

var (
	errAutoModerationBaseURLEmpty  = errors.New("auto moderation base URL is empty")
	errAutoModerationMissingAPIKey = errors.New("AUTO_MODERATION_ENABLED is true, but NIM_API_KEY is not configured")
	errNIMResponseMissingChoices   = errors.New("nim response missing choices")
)

func NewWorker(db *gorm.DB, redisClient *appredis.Client) *Worker {
	if db == nil {
		return nil
	}

	cfg := loadConfigFromEnv()
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil
	}

	return &Worker{
		db:         db,
		redis:      redisClient,
		client:     &http.Client{Timeout: cfg.Timeout},
		config:     cfg,
		triggerCh:  make(chan string, 256),
		processing: make(chan struct{}, 1),
	}
}

func (w *Worker) Start(ctx context.Context) {
	if w == nil {
		return
	}

	go func() {
		ticker := time.NewTicker(w.config.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w.scheduleSweep("")
			case reportID := <-w.triggerCh:
				w.scheduleSweep(reportID)
			}
		}
	}()
}

func (w *Worker) Enqueue(reportID string) {
	if w == nil {
		return
	}

	select {
	case w.triggerCh <- strings.TrimSpace(reportID):
	default:
		log.Printf("auto moderation queue full, dropping trigger for report %q", reportID)
	}
}

func (w *Worker) scheduleSweep(reportID string) {
	select {
	case w.processing <- struct{}{}:
		go func() {
			defer func() { <-w.processing }()
			if err := w.processPendingReports(context.Background(), reportID); err != nil {
				log.Printf("auto moderation sweep failed: %v", err)
			}
		}()
	default:
	}
}

func (w *Worker) processPendingReports(ctx context.Context, prioritizedReportID string) error {
	enabled, err := EnabledSetting(ctx, w.db, w.config.EnabledDefault)
	if err != nil {
		return fmt.Errorf("load auto moderation setting: %w", err)
	}
	if !enabled {
		return nil
	}
	if strings.TrimSpace(w.config.APIKey) == "" {
		return errAutoModerationMissingAPIKey
	}

	reports, err := w.claimReports(ctx, prioritizedReportID)
	if err != nil {
		return err
	}
	if len(reports) == 0 {
		return nil
	}

	for _, report := range reports {
		if err := w.processSingleReport(ctx, report); err != nil {
			log.Printf("auto moderation report %s failed: %v", report.ID, err)
		}
	}

	return nil
}

func (w *Worker) claimReports(ctx context.Context, prioritizedReportID string) ([]storage.Report, error) {
	now := time.Now()
	staleBefore := now.Add(-w.config.ClaimTimeout)
	claimed := make([]storage.Report, 0, w.config.BatchSize)

	err := w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Model(&storage.Report{}).
			Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ?", statePending).
			Where(
				tx.Where("auto_moderation_state = ?", statePending).
					Or("auto_moderation_state = ? AND auto_moderation_claimed_at < ?", stateProcessing, staleBefore).
					Or("auto_moderation_state = ? AND auto_moderation_attempts < ?", stateFailed, w.config.MaxAttempts),
			)

		if prioritizedReportID != "" {
			query = query.Order(clause.Expr{SQL: "CASE WHEN id = ? THEN 0 ELSE 1 END", Vars: []any{prioritizedReportID}})
		}

		if err := query.Order("created_at ASC").Limit(w.config.BatchSize).Find(&claimed).Error; err != nil {
			return err
		}
		if len(claimed) == 0 {
			return nil
		}

		ids := make([]string, 0, len(claimed))
		for _, report := range claimed {
			ids = append(ids, report.ID)
		}

		return tx.Model(&storage.Report{}).
			Where("id IN ?", ids).
			Updates(map[string]any{
				"auto_moderation_state":      stateProcessing,
				"auto_moderation_claimed_at": now,
				"auto_moderation_error":      "",
				"auto_moderation_attempts":   gorm.Expr("auto_moderation_attempts + 1"),
			}).Error
	})

	return claimed, err
}

func (w *Worker) processSingleReport(ctx context.Context, report storage.Report) error {
	peerEvidence, peerMessageCount := extractPeerEvidence(report.ChatLog)
	decision := decisionEscalate
	categories := make([]string, 0)
	summary := "Escalated for human review because the report did not include enough text evidence."
	completedAt := time.Now()

	reporterEvidence, reporterMessageCount := extractReporterEvidence(report.ChatLog)

	modelAdapter, err := models.Resolve(w.config.ModelType)
	if err != nil {
		return w.markFailure(ctx, report.ID, err, false)
	}

	if dualAdapter, ok := modelAdapter.(shared.DualAssessmentAdapter); ok && (peerMessageCount > 0 || reporterMessageCount > 0 || strings.TrimSpace(report.Description) != "") {
		messages := dualAdapter.BuildDualMessages(report, peerEvidence, reporterEvidence)
		raw, err := w.callModelAPIMulti(ctx, report, messages)
		if err != nil {
			return w.markFailure(ctx, report.ID, err, isRetryableAssessmentError(err))
		}
		dualAssessment, err := dualAdapter.ParseDualAssessment(raw)
		if err != nil {
			return w.markFailure(ctx, report.ID, err, isRetryableAssessmentError(err))
		}

		reporterBanned := false
		if reporterMessageCount > 0 && dualAssessment.Reporter.UserSafety == "unsafe" {
			w.silentBanReporter(ctx, report, dualAssessment.Reporter.Categories, completedAt)
			reporterBanned = true
		}

		decision, summary = determineDecision(safetyAssessment{
			UserSafety:     dualAssessment.ReportedUser.UserSafety,
			ResponseSafety: dualAssessment.ReportedUser.ResponseSafety,
			Categories:     dualAssessment.ReportedUser.Categories,
		})
		if reporterBanned {
			summary += " Note: The reporter was found to be abusive and was automatically counter-banned."
		}
		categories = dualAssessment.ReportedUser.Categories
	} else {
		// Fallback for models without DualAssessment support (e.g. Llama Guard)
		if peerMessageCount > 0 || strings.TrimSpace(report.Description) != "" {
			assessment, err := w.assessReport(ctx, report, peerEvidence, modelAdapter)
			if err != nil {
				return w.markFailure(ctx, report.ID, err, isRetryableAssessmentError(err))
			}
			decision, summary = determineDecision(assessment)
			categories = assessment.Categories
		}

		reporterBanned := false
		if reporterMessageCount > 0 {
			reporterAssessment, err := w.assessReport(ctx, report, reporterEvidence, modelAdapter)
			if err != nil {
				log.Printf("auto moderation reporter counter-assessment failed for report %s: %v", report.ID, err)
			} else if reporterAssessment.UserSafety == "unsafe" {
				w.silentBanReporter(ctx, report, reporterAssessment.Categories, completedAt)
				reporterBanned = true
			}
		}
		if reporterBanned {
			summary += " Note: The reporter was found to be abusive and was automatically counter-banned."
		}
	}

	switch decision {
	case decisionApproved:
		return w.completeApprovedReport(ctx, report, categories, summary, completedAt)
	case decisionRejected:
		_, err := w.finalizeDecision(ctx, report.ID, decision, categories, summary, completedAt, "", true)
		return err
	default:
		_, err := w.finalizeDecision(ctx, report.ID, decision, categories, summary, completedAt, "", false)
		return err
	}
}

func (w *Worker) completeApprovedReport(
	ctx context.Context,
	report storage.Report,
	categories []string,
	summary string,
	completedAt time.Time,
) error {
	banID := ""
	if strings.TrimSpace(report.ReportedSessionID) != "" || strings.TrimSpace(report.ReportedIP) != "" {
		expiresAt := completedAt.Add(autoBanDuration)
		banResult, banSummary, err := w.createAutomaticBan(ctx, report, categories, &expiresAt)
		if err != nil {
			return w.markFailure(ctx, report.ID, err, true)
		}
		if banResult.Ban.ID != "" && !banResult.AlreadyBanned {
			banID = banResult.Ban.ID
		}
		if banSummary != "" {
			summary = summary + " " + banSummary
		}
	}

	updated, err := w.finalizeDecision(ctx, report.ID, decisionApproved, categories, summary, completedAt, banID, true)
	if err != nil {
		if banID != "" {
			_ = w.rollbackAutomaticBan(ctx, banID)
		}
		return err
	}
	if updated {
		return nil
	}
	if banID != "" {
		_ = w.rollbackAutomaticBan(ctx, banID)
	}
	return nil
}

func (w *Worker) finalizeDecision(
	ctx context.Context,
	reportID string,
	decision string,
	categories []string,
	summary string,
	completedAt time.Time,
	banID string,
	autoReview bool,
) (bool, error) {
	updates := map[string]any{
		"auto_moderation_state":        stateCompleted,
		"auto_moderation_decision":     decision,
		"auto_moderation_categories":   mustMarshalCategories(categories),
		"auto_moderation_summary":      summary,
		"auto_moderation_error":        "",
		"auto_moderation_model":        w.config.Model,
		"auto_moderation_completed_at": completedAt,
		"auto_moderation_claimed_at":   nil,
		"auto_moderation_ban_id":       banID,
	}

	if !autoReview {
		return true, w.db.WithContext(ctx).Model(&storage.Report{}).Where("id = ?", reportID).Updates(updates).Error
	}

	reviewedAt := time.Now()
	reviewUpdates := make(map[string]any, len(updates)+3)
	for key, value := range updates {
		reviewUpdates[key] = value
	}
	reviewUpdates["status"] = decision
	reviewUpdates["reviewed_by_username"] = ReviewerName
	reviewUpdates["reviewed_at"] = reviewedAt

	tx := w.db.WithContext(ctx).Model(&storage.Report{}).
		Where("id = ? AND status = ?", reportID, statePending).
		Updates(reviewUpdates)
	if tx.Error != nil {
		return false, tx.Error
	}
	if tx.RowsAffected > 0 {
		return true, nil
	}

	return false, w.db.WithContext(ctx).Model(&storage.Report{}).Where("id = ?", reportID).Updates(updates).Error
}

func (w *Worker) rollbackAutomaticBan(ctx context.Context, banID string) error {
	if strings.TrimSpace(banID) == "" {
		return nil
	}

	rollbackCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	_, err := moderation.DeleteBan(rollbackCtx, w.db, w.redis, banID, ReviewerName)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	return err
}

func (w *Worker) createAutomaticBan(
	ctx context.Context,
	report storage.Report,
	categories []string,
	expiresAt *time.Time,
) (moderation.CreateBanResult, string, error) {
	reason := autoBanReason(report, categories)
	banResult, err := moderation.CreateOrRefreshBan(ctx, w.db, w.redis, moderation.CreateBanParams{
		SessionID:        strings.TrimSpace(report.ReportedSessionID),
		IPAddress:        strings.TrimSpace(report.ReportedIP),
		Reason:           reason,
		BannedByUsername: ReviewerName,
		ExpiresAt:        expiresAt,
	})
	if err != nil {
		return moderation.CreateBanResult{}, "", fmt.Errorf("auto ban reported user: %w", err)
	}

	if banResult.AlreadyBanned {
		return banResult, "The reported user already had an active ban, so auto moderation kept the existing ban in place.", nil
	}

	if expiresAt == nil {
		return banResult, "Applied an automatic ban.", nil
	}

	return banResult, fmt.Sprintf("Applied a 7-day temporary ban until %s.", expiresAt.UTC().Format(time.RFC3339)), nil
}

func (w *Worker) silentBanReporter(ctx context.Context, report storage.Report, categories []string, completedAt time.Time) {
	sessionID := strings.TrimSpace(report.ReporterSessionID)
	ip := strings.TrimSpace(report.ReporterIP)
	if sessionID == "" && ip == "" {
		return
	}

	reason := "Auto-moderation counter-ban: reporter's own messages violated policy"
	if len(categories) > 0 {
		reason = reason + " [" + strings.Join(categories, ", ") + "]"
	}
	reason = truncateForDB(reason, 200)

	expiresAt := completedAt.Add(autoBanDuration)
	_, err := moderation.CreateOrRefreshBan(ctx, w.db, w.redis, moderation.CreateBanParams{
		SessionID:        sessionID,
		IPAddress:        ip,
		Reason:           reason,
		BannedByUsername: ReviewerName,
		ExpiresAt:        &expiresAt,
	})
	if err != nil {
		log.Printf("auto moderation reporter counter-ban failed for report %s: %v", report.ID, err)
		return
	}

	log.Printf("auto moderation reporter counter-ban applied for report %s (session=%s ip=%s)", report.ID, sessionID, ip)
}

func autoBanReason(report storage.Report, categories []string) string {
	reason := "Auto-moderation temporary ban"
	if strings.TrimSpace(report.Reason) != "" {
		reason = reason + ": " + safePromptText(report.Reason)
	}
	if len(categories) > 0 {
		reason = reason + " [" + strings.Join(categories, ", ") + "]"
	}
	return truncateForDB(reason, 200)
}

func (w *Worker) markFailure(ctx context.Context, reportID string, err error, retryable bool) error {
	errorMessage := err.Error()
	if retryable {
		errorMessage = "Transient auto moderation error; will retry automatically: " + err.Error()
	}

	failure := map[string]any{
		"auto_moderation_state":      stateFailed,
		"auto_moderation_error":      truncateForDB(errorMessage, 500),
		"auto_moderation_claimed_at": nil,
	}
	if !retryable {
		failure["auto_moderation_attempts"] = w.config.MaxAttempts
	}
	if updateErr := w.db.WithContext(ctx).Model(&storage.Report{}).Where("id = ?", reportID).Updates(failure).Error; updateErr != nil {
		return errors.Join(err, updateErr)
	}
	return err
}

func (w *Worker) assessReport(ctx context.Context, report storage.Report, peerEvidence string, modelAdapter shared.Adapter) (safetyAssessment, error) {
	prompt := modelAdapter.BuildPrompt(report, peerEvidence)

	rawResponse, err := w.callModelAPI(ctx, report, prompt)
	if err != nil {
		return safetyAssessment{}, err
	}

	assessment, err := modelAdapter.ParseAssessment(rawResponse)
	if err != nil {
		return safetyAssessment{}, err
	}

	return safetyAssessment{
		UserSafety:     assessment.UserSafety,
		ResponseSafety: assessment.ResponseSafety,
		Categories:     assessment.Categories,
	}, nil
}

func (w *Worker) callModelAPI(ctx context.Context, report storage.Report, prompt string) (string, error) {
	baseURL := normalizeOpenAIBaseURL(w.config.BaseURL)
	if baseURL == "" {
		return "", errAutoModerationBaseURLEmpty
	}

	client := openai.NewClient(
		option.WithAPIKey(w.config.APIKey),
		option.WithBaseURL(baseURL),
		option.WithHTTPClient(w.client),
	)

	requestCtx, cancel := context.WithTimeout(ctx, w.config.Timeout)
	defer cancel()

	completion, err := client.Chat.Completions.New(requestCtx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage(prompt),
		},
		Model:       openai.ChatModel(w.config.Model),
		Temperature: openai.Float(w.config.Temperature),
		MaxTokens:   openai.Int(w.config.MaxTokens),
	})
	if err != nil {
		return "", fmt.Errorf("nim api %s: %w", baseURL, err)
	}
	if len(completion.Choices) == 0 {
		return "", errNIMResponseMissingChoices
	}

	rawResponse := completion.Choices[0].Message.Content
	if w.config.DebugLogging {
		fmt.Printf("\n=== AUTO-MODERATION DEBUG (report %s) ===\n", report.ID)
		fmt.Printf("--- USAGE ---\nInput: %d | Output: %d | Total: %d tokens\n",
			completion.Usage.PromptTokens,
			completion.Usage.CompletionTokens,
			completion.Usage.TotalTokens,
		)
		fmt.Printf("--- PROMPT ---\n%s\n", prompt)
		fmt.Printf("--- RAW RESPONSE ---\n%s\n", rawResponse)
		fmt.Printf("=== END DEBUG ===\n\n")
	}

	return rawResponse, nil
}

func (w *Worker) callModelAPIMulti(ctx context.Context, report storage.Report, coreMessages []shared.CoreMessage) (string, error) {
	baseURL := normalizeOpenAIBaseURL(w.config.BaseURL)
	if baseURL == "" {
		return "", errAutoModerationBaseURLEmpty
	}

	client := openai.NewClient(
		option.WithAPIKey(w.config.APIKey),
		option.WithBaseURL(baseURL),
		option.WithHTTPClient(w.client),
	)

	requestCtx, cancel := context.WithTimeout(ctx, w.config.Timeout)
	defer cancel()

	var apiMessages []openai.ChatCompletionMessageParamUnion
	for _, m := range coreMessages {
		if m.Role == "user" {
			apiMessages = append(apiMessages, openai.UserMessage(m.Content))
		} else if m.Role == "assistant" {
			apiMessages = append(apiMessages, openai.AssistantMessage(m.Content))
		} else if m.Role == "system" {
			apiMessages = append(apiMessages, openai.SystemMessage(m.Content))
		}
	}

	completion, err := client.Chat.Completions.New(requestCtx, openai.ChatCompletionNewParams{
		Messages:    apiMessages,
		Model:       openai.ChatModel(w.config.Model),
		Temperature: openai.Float(w.config.Temperature),
		MaxTokens:   openai.Int(w.config.MaxTokens),
	})
	if err != nil {
		return "", fmt.Errorf("nim api %s: %w", baseURL, err)
	}
	if len(completion.Choices) == 0 {
		return "", errNIMResponseMissingChoices
	}

	rawResponse := completion.Choices[0].Message.Content
	if w.config.DebugLogging {
		fmt.Printf("\n=== AUTO-MODERATION DEBUG (report %s) ===\n", report.ID)
		fmt.Printf("--- USAGE ---\nInput: %d | Output: %d | Total: %d tokens\n",
			completion.Usage.PromptTokens,
			completion.Usage.CompletionTokens,
			completion.Usage.TotalTokens,
		)
		for i, m := range coreMessages {
			fmt.Printf("--- MESSAGE %d (%s) ---\n%s\n", i, m.Role, m.Content)
		}
		fmt.Printf("--- RAW RESPONSE ---\n%s\n", rawResponse)
		fmt.Printf("=== END DEBUG ===\n\n")
	}

	return rawResponse, nil
}

func normalizeOpenAIBaseURL(baseURL string) string {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return ""
	}

	trimmed = strings.TrimRight(trimmed, "/")
	if strings.HasSuffix(trimmed, "/chat/completions") {
		return strings.TrimSuffix(trimmed, "/chat/completions")
	}
	if strings.HasSuffix(trimmed, "/v1") {
		return trimmed
	}
	return trimmed + "/v1"
}

func SettingsStatus(ctx context.Context, db *gorm.DB) (StatusResponse, error) {
	cfg := loadConfigFromEnv()
	enabled, err := EnabledSetting(ctx, db, cfg.EnabledDefault)
	if err != nil {
		return StatusResponse{}, err
	}

	return StatusResponse{
		Enabled:         enabled,
		EnabledDefault:  cfg.EnabledDefault,
		Configured:      strings.TrimSpace(cfg.APIKey) != "",
		Model:           cfg.Model,
		ModelType:       cfg.ModelType,
		BatchSize:       cfg.BatchSize,
		IntervalSeconds: int(cfg.Interval.Seconds()),
		TimeoutSeconds:  int(cfg.Timeout.Seconds()),
		MaxAttempts:     cfg.MaxAttempts,
	}, nil
}

func EnabledSetting(ctx context.Context, db *gorm.DB, defaultValue bool) (bool, error) {
	var setting storage.AdminSetting
	err := db.WithContext(ctx).Where("key = ?", EnabledSettingKey).First(&setting).Error
	if err == nil {
		return parseBoolWithDefault(setting.Value, defaultValue), nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return defaultValue, nil
	}
	return defaultValue, err
}

func SetEnabledSetting(ctx context.Context, db *gorm.DB, username string, enabled bool) error {
	setting := storage.AdminSetting{
		Key:               EnabledSettingKey,
		Value:             boolToSettingValue(enabled),
		UpdatedByUsername: username,
	}

	return db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_by_username", "updated_at"}),
	}).Create(&setting).Error
}

func extractEvidence(raw string, senderFilter string) (string, int) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return "", 0
	}

	var messages []chatLogMessage
	if err := json.Unmarshal([]byte(raw), &messages); err != nil {
		return "", 0
	}

	lines := make([]string, 0, len(messages))
	for _, msg := range messages {
		if msg.Sender != senderFilter {
			continue
		}
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			continue
		}
		lines = append(lines, "- "+safePromptText(text))
	}

	return strings.Join(lines, "\n"), len(lines)
}

func extractPeerEvidence(raw string) (string, int) {
	return extractEvidence(raw, "peer")
}

func extractReporterEvidence(raw string) (string, int) {
	return extractEvidence(raw, "me")
}

func determineDecision(assessment safetyAssessment) (string, string) {
	if assessment.UserSafety == "unsafe" {
		if len(assessment.Categories) == 0 {
			return decisionApproved, "Approved automatically because the model marked the reported content unsafe."
		}
		return decisionApproved, "Approved automatically because the model marked the reported content unsafe in categories: " + strings.Join(assessment.Categories, ", ")
	}

	if assessment.UserSafety == "safe" {
		return decisionRejected, "Rejected automatically because the model marked the reported content safe."
	}

	return decisionEscalate, "Escalated for human review because the model response could not be classified confidently."
}

func isRetryableAssessmentError(err error) bool {
	if err == nil {
		return false
	}

	switch {
	case errors.Is(err, errAutoModerationBaseURLEmpty),
		errors.Is(err, errAutoModerationMissingAPIKey),
		errors.Is(err, models.ErrUnsupportedModel):
		return false
	case errors.Is(err, context.Canceled):
		return false
	case errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, errNIMResponseMissingChoices):
		return true
	}

	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusUnprocessableEntity:
			return false
		default:
			return true
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	return true
}

func loadConfigFromEnv() Config {
	rawModel := strings.TrimSpace(defaultString(firstNonEmptyEnv("AUTO_MODERATION_MODEL"), defaultModel))
	return Config{
		EnabledDefault: parseBoolWithDefault(firstNonEmptyEnv("AUTO_MODERATION_ENABLED"), false),
		APIKey:         strings.TrimSpace(firstNonEmptyEnv("NIM_API_KEY", "NVIDIA_NIM_API_KEY", "AUTO_MODERATION_NIM_API_KEY")),
		BaseURL:        strings.TrimSpace(defaultString(firstNonEmptyEnv("AUTO_MODERATION_NIM_BASE_URL"), defaultAPIBaseURL)),
		Model:          rawModel,
		ModelType:      strings.TrimSpace(defaultString(firstNonEmptyEnv("AUTO_MODERATION_MODEL_TYPE"), rawModel)),
		BatchSize:      boundedInt(firstNonEmptyEnv("AUTO_MODERATION_BATCH_SIZE"), 10, 1, 100),
		Interval:       time.Duration(boundedInt(firstNonEmptyEnv("AUTO_MODERATION_BATCH_INTERVAL_SECONDS"), 30, 5, 3600)) * time.Second,
		Timeout:        time.Duration(boundedInt(firstNonEmptyEnv("AUTO_MODERATION_TIMEOUT_SECONDS"), 20, 5, 120)) * time.Second,
		ClaimTimeout:   time.Duration(boundedInt(firstNonEmptyEnv("AUTO_MODERATION_CLAIM_TIMEOUT_SECONDS"), 300, 30, 3600)) * time.Second,
		MaxAttempts:    boundedInt(firstNonEmptyEnv("AUTO_MODERATION_MAX_ATTEMPTS"), 3, 1, 10),
		Temperature:    boundedFloat(firstNonEmptyEnv("AUTO_MODERATION_TEMPERATURE"), 0.5, 0.0, 2.0),
		MaxTokens:      int64(boundedInt(firstNonEmptyEnv("AUTO_MODERATION_MAX_TOKENS"), 8192, 1, 128000)),
		DebugLogging:   parseBoolWithDefault(firstNonEmptyEnv("AUTO_MODERATION_DEBUG"), false),
	}
}

func mustMarshalCategories(categories []string) string {
	if len(categories) == 0 {
		return "[]"
	}
	encoded, err := json.Marshal(categories)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func parseBoolWithDefault(raw string, defaultValue bool) bool {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" {
		return defaultValue
	}
	switch trimmed {
	case "1", "true", "on", "yes", "enabled":
		return true
	case "0", "false", "off", "no", "disabled":
		return false
	default:
		return defaultValue
	}
}

func boolToSettingValue(enabled bool) string {
	if enabled {
		return "true"
	}
	return "false"
}

func safePromptText(input string) string {
	cleaned := strings.ReplaceAll(input, "\r", " ")
	cleaned = strings.ReplaceAll(cleaned, "\n", " ")
	return truncateForDB(strings.TrimSpace(cleaned), 4000)
}

func truncateForDB(input string, maxLen int) string {
	if maxLen <= 0 || len(input) <= maxLen {
		return input
	}
	return input[:maxLen]
}

func boundedInt(raw string, defaultValue, minValue, maxValue int) int {
	if raw == "" {
		return defaultValue
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return defaultValue
	}
	if parsed < minValue {
		return minValue
	}
	if parsed > maxValue {
		return maxValue
	}
	return parsed
}

func boundedFloat(raw string, defaultValue, minValue, maxValue float64) float64 {
	if raw == "" {
		return defaultValue
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return defaultValue
	}
	if parsed < minValue {
		return minValue
	}
	if parsed > maxValue {
		return maxValue
	}
	return parsed
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func firstNonEmptyEnv(keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
