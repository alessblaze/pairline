package automod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

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
)

type Config struct {
	EnabledDefault bool
	APIKey         string
	BaseURL        string
	Model          string
	BatchSize      int
	Interval       time.Duration
	Timeout        time.Duration
	ClaimTimeout   time.Duration
	MaxAttempts    int
}

type Worker struct {
	db         *gorm.DB
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

func NewWorker(db *gorm.DB) *Worker {
	if db == nil {
		return nil
	}

	cfg := loadConfigFromEnv()
	return &Worker{
		db:         db,
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
		return errors.New("AUTO_MODERATION_ENABLED is true, but NIM_API_KEY is not configured")
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

	if peerMessageCount > 0 || strings.TrimSpace(report.Description) != "" {
		assessment, err := w.assessReport(ctx, report, peerEvidence)
		if err != nil {
			return w.markFailure(ctx, report.ID, err)
		}

		decision, summary = determineDecision(assessment)
		categories = assessment.Categories
	}

	updates := map[string]any{
		"auto_moderation_state":        stateCompleted,
		"auto_moderation_decision":     decision,
		"auto_moderation_categories":   mustMarshalCategories(categories),
		"auto_moderation_summary":      summary,
		"auto_moderation_error":        "",
		"auto_moderation_model":        w.config.Model,
		"auto_moderation_completed_at": completedAt,
		"auto_moderation_claimed_at":   nil,
	}

	if err := w.db.WithContext(ctx).Model(&storage.Report{}).Where("id = ?", report.ID).Updates(updates).Error; err != nil {
		return err
	}

	if decision != decisionApproved && decision != decisionRejected {
		return nil
	}

	reviewedAt := time.Now()
	return w.db.WithContext(ctx).Model(&storage.Report{}).
		Where("id = ? AND status = ?", report.ID, statePending).
		Updates(map[string]any{
			"status":               decision,
			"reviewed_by_username": ReviewerName,
			"reviewed_at":          reviewedAt,
		}).Error
}

func (w *Worker) markFailure(ctx context.Context, reportID string, err error) error {
	failure := map[string]any{
		"auto_moderation_state":      stateFailed,
		"auto_moderation_error":      truncateForDB(err.Error(), 500),
		"auto_moderation_claimed_at": nil,
	}
	if updateErr := w.db.WithContext(ctx).Model(&storage.Report{}).Where("id = ?", reportID).Updates(failure).Error; updateErr != nil {
		return errors.Join(err, updateErr)
	}
	return err
}

func (w *Worker) assessReport(ctx context.Context, report storage.Report, peerEvidence string) (safetyAssessment, error) {
	prompt := buildPrompt(report, peerEvidence)

	baseURL := normalizeOpenAIBaseURL(w.config.BaseURL)
	if baseURL == "" {
		return safetyAssessment{}, errors.New("auto moderation base URL is empty")
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
		Temperature: openai.Float(0),
		MaxTokens:   openai.Int(250),
	})
	if err != nil {
		return safetyAssessment{}, fmt.Errorf("nim api %s: %w", baseURL, err)
	}
	if len(completion.Choices) == 0 {
		return safetyAssessment{}, errors.New("nim response missing choices")
	}

	return parseAssessment(completion.Choices[0].Message.Content)
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

func buildPrompt(report storage.Report, peerEvidence string) string {
	var b strings.Builder
	b.WriteString("Task: Check if there is unsafe content in the reported user's messages and the report evidence in accordance with our safety policy below. ")
	b.WriteString("Provide a 'safe' or 'unsafe' rating for the reported user content only. If there is unsafe content, provide a comma-separated list of violated categories. ")
	b.WriteString("If the evidence is too limited to support an unsafe finding, mark it safe.\n")
	b.WriteString("<BEGIN UNSAFE CONTENT CATEGORIES>\n")
	b.WriteString("S1: Violence.\nS2: Sexual.\nS3: Criminal Planning/Confessions.\nS4: Guns and Illegal Weapons.\n")
	b.WriteString("S5: Controlled/Regulated Substances.\nS6: Suicide and Self Harm.\nS7: Sexual (minor).\n")
	b.WriteString("S8: Hate/Identity Hate.\nS9: PII/Privacy.\nS10: Harassment.\nS11: Threat.\nS12: Profanity.\n")
	b.WriteString("S13: Needs Caution.\nS14: Other.\nS15: Manipulation.\nS16: Fraud/Deception.\nS17: Malware.\n")
	b.WriteString("S18: High Risk Gov Decision Making.\nS19: Political/Misinformation/Conspiracy.\n")
	b.WriteString("S20: Copyright/Trademark/Plagiarism.\nS21: Unauthorized Advice.\nS22: Illegal Activity.\n")
	b.WriteString("S23: Immoral/Unethical.\n")
	b.WriteString("<END UNSAFE CONTENT CATEGORIES>\n")
	b.WriteString("<BEGIN CONVERSATION>\n")
	b.WriteString("user: Review the following report evidence about a reported chat participant.\n")
	b.WriteString("Report reason: " + safePromptText(report.Reason) + "\n")
	if description := strings.TrimSpace(report.Description); description != "" {
		b.WriteString("Reporter description: " + safePromptText(description) + "\n")
	}
	if peerEvidence != "" {
		b.WriteString("Messages from reported user:\n")
		b.WriteString(peerEvidence)
		b.WriteString("\n")
	}
	b.WriteString("<END CONVERSATION>\n")
	b.WriteString("Provide your safety assessment for the above report in the below JSON format:\n")
	b.WriteString("{\"User Safety\": \"safe\" or \"unsafe\", \"Safety Categories\": \"comma-separated list of applicable categories when unsafe\"}\n")
	b.WriteString("Do not include anything other than the output JSON in your response.\n")
	return b.String()
}

func extractPeerEvidence(raw string) (string, int) {
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
		if msg.Sender != "peer" {
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

func parseAssessment(raw string) (safetyAssessment, error) {
	type rawAssessment struct {
		UserSafety     string `json:"User Safety"`
		ResponseSafety string `json:"Response Safety,omitempty"`
		Categories     string `json:"Safety Categories,omitempty"`
	}

	jsonBody, err := extractJSONObject(raw)
	if err != nil {
		return safetyAssessment{}, err
	}

	var parsed rawAssessment
	if err := json.Unmarshal([]byte(jsonBody), &parsed); err != nil {
		return safetyAssessment{}, err
	}

	assessment := safetyAssessment{
		UserSafety:     strings.ToLower(strings.TrimSpace(parsed.UserSafety)),
		ResponseSafety: strings.ToLower(strings.TrimSpace(parsed.ResponseSafety)),
		Categories:     normalizeCategories(parsed.Categories),
	}
	if assessment.UserSafety != "safe" && assessment.UserSafety != "unsafe" {
		return safetyAssessment{}, fmt.Errorf("unexpected user safety value %q", parsed.UserSafety)
	}

	return assessment, nil
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

func loadConfigFromEnv() Config {
	return Config{
		EnabledDefault: parseBoolWithDefault(firstNonEmptyEnv("AUTO_MODERATION_ENABLED"), false),
		APIKey:         strings.TrimSpace(firstNonEmptyEnv("NIM_API_KEY", "NVIDIA_NIM_API_KEY", "AUTO_MODERATION_NIM_API_KEY")),
		BaseURL:        strings.TrimSpace(defaultString(firstNonEmptyEnv("AUTO_MODERATION_NIM_BASE_URL"), defaultAPIBaseURL)),
		Model:          strings.TrimSpace(defaultString(firstNonEmptyEnv("AUTO_MODERATION_MODEL"), defaultModel)),
		BatchSize:      boundedInt(firstNonEmptyEnv("AUTO_MODERATION_BATCH_SIZE"), 10, 1, 100),
		Interval:       time.Duration(boundedInt(firstNonEmptyEnv("AUTO_MODERATION_BATCH_INTERVAL_SECONDS"), 30, 5, 3600)) * time.Second,
		Timeout:        time.Duration(boundedInt(firstNonEmptyEnv("AUTO_MODERATION_TIMEOUT_SECONDS"), 20, 5, 120)) * time.Second,
		ClaimTimeout:   time.Duration(boundedInt(firstNonEmptyEnv("AUTO_MODERATION_CLAIM_TIMEOUT_SECONDS"), 300, 30, 3600)) * time.Second,
		MaxAttempts:    boundedInt(firstNonEmptyEnv("AUTO_MODERATION_MAX_ATTEMPTS"), 3, 1, 10),
	}
}

func extractJSONObject(raw string) (string, error) {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start == -1 || end == -1 || end < start {
		return "", errors.New("model response did not contain a JSON object")
	}
	return raw[start : end+1], nil
}

func normalizeCategories(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	categories := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(strings.ToLower(part))
		if trimmed != "" {
			categories = append(categories, trimmed)
		}
	}
	return categories
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
