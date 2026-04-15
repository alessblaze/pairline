package shared

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/anish/omegle/backend/golang/internal/automod/taxonomy"
	"github.com/anish/omegle/backend/golang/internal/storage"
)

var ErrModelResponseMissingJSON = errors.New("model response did not contain a JSON object")

var codePattern = regexp.MustCompile(`(?i)\b(s\d+)\b`)

type Assessment struct {
	UserSafety     string
	ResponseSafety string
	Categories     []string
}

type Adapter interface {
	Matches(model string) bool
	BuildPrompt(report storage.Report, peerEvidence string) string
	ParseAssessment(raw string) (Assessment, error)
}

// DualAssessment holds safety verdicts for both the reported user and the reporter.
type DualAssessment struct {
	ReportedUser Assessment
	Reporter     Assessment
}

// CoreMessage represents a single message in a chat history for the model payload.
type CoreMessage struct {
	Role    string
	Content string
}

// DualAssessmentAdapter is an optional interface that adapters can implement
// to assess both participants in a single API call, saving tokens and
// providing better context for the model.
type DualAssessmentAdapter interface {
	BuildDualMessages(report storage.Report, reportedEvidence, reporterEvidence string) []CoreMessage
	ParseDualAssessment(raw string) (DualAssessment, error)
}

func NormalizeModelID(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

func ExtractJSONObject(raw string) (string, error) {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start == -1 || end == -1 || end < start {
		return "", ErrModelResponseMissingJSON
	}
	return raw[start : end+1], nil
}

func NormalizeCategories(raw string) []string {
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

func BuildJSONSafetyPrompt(report storage.Report, peerEvidence string, includeReportReason bool) string {
	var b strings.Builder
	b.WriteString("Task: Check if there is unsafe content in the reported user's messages and the report evidence in accordance with our safety policy below. ")
	b.WriteString("Provide a 'safe' or 'unsafe' rating for the reported user content only. If there is unsafe content, provide a comma-separated list of violated categories. ")
	b.WriteString("If the evidence is too limited to support an unsafe finding, mark it safe.\n")
	b.WriteString("<BEGIN UNSAFE CONTENT CATEGORIES>\n")
	b.WriteString(taxonomy.Render())
	b.WriteString("<END UNSAFE CONTENT CATEGORIES>\n")
	b.WriteString("<BEGIN CONVERSATION>\n")
	b.WriteString("user: Review the following report evidence about a reported chat participant.\n")
	if includeReportReason {
		if strings.TrimSpace(report.Reason) != "" {
			b.WriteString("Report reason: " + SanitizePromptText(report.Reason) + "\n")
		}
		if description := strings.TrimSpace(report.Description); description != "" {
			b.WriteString("Reporter description: " + SanitizePromptText(description) + "\n")
		}
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

func ParseJSONSafetyAssessment(raw string) (Assessment, error) {
	type rawAssessment struct {
		UserSafety     string `json:"User Safety"`
		ResponseSafety string `json:"Response Safety,omitempty"`
		Categories     string `json:"Safety Categories,omitempty"`
	}

	jsonBody, err := ExtractJSONObject(raw)
	if err != nil {
		return Assessment{}, err
	}

	var parsed rawAssessment
	if err := json.Unmarshal([]byte(jsonBody), &parsed); err != nil {
		return Assessment{}, err
	}

	assessment := Assessment{
		UserSafety:     strings.ToLower(strings.TrimSpace(parsed.UserSafety)),
		ResponseSafety: strings.ToLower(strings.TrimSpace(parsed.ResponseSafety)),
		Categories:     taxonomy.CanonicalizeMany(NormalizeCategories(parsed.Categories)),
	}
	if assessment.UserSafety != "safe" && assessment.UserSafety != "unsafe" {
		return Assessment{}, fmt.Errorf("unexpected user safety value %q", parsed.UserSafety)
	}

	return assessment, nil
}

func BuildDualJSONSafetyPrompt(report storage.Report, reportedEvidence string, reporterEvidence string, includeReportReason bool) string {
	var b strings.Builder
	b.WriteString("Task: Check if there is unsafe content in the given chat log in accordance with our safety policy below. ")
	b.WriteString("Assess the messages sent by both the 'Reported User' and the 'Reporter' independently. ")
	b.WriteString("Provide a 'safe' or 'unsafe' rating for each user. If either user has unsafe content, provide a comma-separated list of violated categories. ")
	b.WriteString("If a user's evidence is too limited or missing, mark them safe.\n")
	b.WriteString("<BEGIN UNSAFE CONTENT CATEGORIES>\n")
	b.WriteString(taxonomy.Render())
	b.WriteString("<END UNSAFE CONTENT CATEGORIES>\n")
	b.WriteString("<BEGIN CONVERSATION>\n")
	if includeReportReason && strings.TrimSpace(report.Reason) != "" {
		b.WriteString("Report reason context: " + SanitizePromptText(report.Reason) + "\n")
	}
	b.WriteString("\nReported User Evidence:\n")
	if reportedEvidence != "" {
		b.WriteString(reportedEvidence + "\n")
	} else {
		b.WriteString("- None provided\n")
	}
	b.WriteString("\nReporter Evidence:\n")
	if reporterEvidence != "" {
		b.WriteString(reporterEvidence + "\n")
	} else {
		b.WriteString("- None provided\n")
	}
	b.WriteString("<END CONVERSATION>\n")
	b.WriteString("Provide your assessment in the following strict JSON format:\n")
	b.WriteString("{\"Reported Safety\": \"safe\" or \"unsafe\", \"Reported Categories\": \"comma-separated list of applicable categories when unsafe\", ")
	b.WriteString("\"Reporter Safety\": \"safe\" or \"unsafe\", \"Reporter Categories\": \"comma-separated list of applicable categories when unsafe\"}\n")
	b.WriteString("Do not include anything other than the output JSON in your response.\n")
	return b.String()
}

func ParseDualJSONSafetyAssessment(raw string) (DualAssessment, error) {
	type rawDualAssessment struct {
		ReportedSafety     string `json:"Reported Safety"`
		ReportedCategories string `json:"Reported Categories,omitempty"`
		ReporterSafety     string `json:"Reporter Safety"`
		ReporterCategories string `json:"Reporter Categories,omitempty"`
	}

	jsonBody, err := ExtractJSONObject(raw)
	if err != nil {
		return DualAssessment{}, err
	}

	var parsed rawDualAssessment
	if err := json.Unmarshal([]byte(jsonBody), &parsed); err != nil {
		return DualAssessment{}, err
	}

	reportedUser := strings.ToLower(strings.TrimSpace(parsed.ReportedSafety))
	reporterUser := strings.ToLower(strings.TrimSpace(parsed.ReporterSafety))

	if reportedUser != "safe" && reportedUser != "unsafe" {
		return DualAssessment{}, fmt.Errorf("unexpected reported safety value %q", parsed.ReportedSafety)
	}
	if reporterUser != "safe" && reporterUser != "unsafe" {
		return DualAssessment{}, fmt.Errorf("unexpected reporter safety value %q", parsed.ReporterSafety)
	}

	return DualAssessment{
		ReportedUser: Assessment{
			UserSafety: reportedUser,
			Categories: taxonomy.CanonicalizeMany(NormalizeCategories(parsed.ReportedCategories)),
		},
		Reporter: Assessment{
			UserSafety: reporterUser,
			Categories: taxonomy.CanonicalizeMany(NormalizeCategories(parsed.ReporterCategories)),
		},
	}, nil
}

func ParsePlaintextSafetyAssessment(raw string) (Assessment, error) {
	trimmed := strings.TrimSpace(stripCodeFences(raw))
	if trimmed == "" {
		return Assessment{}, errors.New("model response did not contain a safety classification")
	}

	lines := strings.Split(trimmed, "\n")
	firstLine := ""
	rest := make([]string, 0, len(lines))
	for _, line := range lines {
		cleaned := strings.TrimSpace(line)
		if cleaned == "" {
			continue
		}
		if firstLine == "" {
			firstLine = strings.ToLower(cleaned)
			continue
		}
		rest = append(rest, cleaned)
	}
	if firstLine != "safe" && firstLine != "unsafe" {
		return Assessment{}, fmt.Errorf("unexpected user safety value %q", firstLine)
	}

	assessment := Assessment{UserSafety: firstLine}
	if firstLine == "unsafe" {
		assessment.Categories = normalizePlaintextCategories(rest)
	}
	return assessment, nil
}

func stripCodeFences(raw string) string {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	return strings.TrimSpace(trimmed)
}

func normalizePlaintextCategories(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}

	categories := make([]string, 0, len(lines))
	for _, line := range lines {
		cleaned := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "-"), "*"))
		if cleaned == "" {
			continue
		}
		if matches := codePattern.FindAllStringSubmatch(cleaned, -1); len(matches) > 0 {
			for _, match := range matches {
				if len(match) > 1 {
					categories = append(categories, strings.ToLower(match[1]))
				}
			}
			continue
		}
		categories = append(categories, NormalizeCategories(strings.ReplaceAll(cleaned, "\n", ","))...)
	}
	if len(categories) == 0 {
		return nil
	}
	return categories
}

func SanitizePromptText(input string) string {
	cleaned := strings.ReplaceAll(input, "\r", " ")
	cleaned = strings.ReplaceAll(cleaned, "\n", " ")
	return truncate(strings.TrimSpace(cleaned), 4000)
}

func truncate(input string, maxLen int) string {
	if maxLen <= 0 || len(input) <= maxLen {
		return input
	}
	return input[:maxLen]
}
