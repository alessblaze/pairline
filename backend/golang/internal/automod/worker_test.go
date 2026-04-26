package automod

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anish/omegle/backend/golang/internal/automod/models"
	"github.com/anish/omegle/backend/golang/internal/storage"
	"github.com/openai/openai-go/v3"
)

func TestExtractPeerEvidenceFiltersPeerMessages(t *testing.T) {
	raw := `[{"text":"hello","sender":"me"},{"text":"go away","sender":"peer"},{"text":"  ","sender":"peer"}]`

	evidence, count := extractPeerEvidence(raw)
	if count != 1 {
		t.Fatalf("extractPeerEvidence() count = %d, want 1", count)
	}
	if evidence != "- go away" {
		t.Fatalf("extractPeerEvidence() evidence = %q", evidence)
	}
}

func TestSafetyGuardModelParsesJSONWrappedInText(t *testing.T) {
	adapter, err := models.Resolve("nvidia/llama-3.1-nemotron-safety-guard-8b-v3")
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}

	assessment, err := adapter.ParseAssessment("Output JSON:\n{\"User Safety\":\"unsafe\",\"Safety Categories\":\"Harassment, Profanity\"}")
	if err != nil {
		t.Fatalf("ParseAssessment() returned error: %v", err)
	}
	if assessment.UserSafety != "unsafe" {
		t.Fatalf("assessment.UserSafety = %q, want %q", assessment.UserSafety, "unsafe")
	}
	if len(assessment.Categories) != 2 || assessment.Categories[0] != "harassment" || assessment.Categories[1] != "profanity" {
		t.Fatalf("assessment.Categories = %#v", assessment.Categories)
	}
}

func TestScheduleSweepRecoversPanicsAndReleasesWorkerSlot(t *testing.T) {
	worker := &Worker{
		processing: make(chan struct{}, 1),
		processPendingReportsFn: func(context.Context, string) error {
			panic("boom")
		},
	}

	var logBuffer bytes.Buffer
	originalWriter := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(&logBuffer)
	log.SetFlags(0)
	defer log.SetOutput(originalWriter)
	defer log.SetFlags(originalFlags)

	worker.scheduleSweep("report-1")

	deadline := time.Now().Add(time.Second)
	for {
		select {
		case worker.processing <- struct{}{}:
			<-worker.processing
			if !strings.Contains(logBuffer.String(), "auto moderation sweep panicked: boom") {
				t.Fatalf("log output = %q, want recovered panic log", logBuffer.String())
			}
			return
		default:
			if time.Now().After(deadline) {
				t.Fatal("processing slot was not released after recovered panic")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestDetermineDecision(t *testing.T) {
	tests := []struct {
		name       string
		assessment safetyAssessment
		want       string
	}{
		{
			name:       "unsafe becomes approved",
			assessment: safetyAssessment{UserSafety: "unsafe", Categories: []string{"harassment"}},
			want:       decisionApproved,
		},
		{
			name:       "safe becomes rejected",
			assessment: safetyAssessment{UserSafety: "safe"},
			want:       decisionRejected,
		},
		{
			name:       "unknown escalates",
			assessment: safetyAssessment{UserSafety: ""},
			want:       decisionEscalate,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := determineDecision(tt.assessment)
			if got != tt.want {
				t.Fatalf("determineDecision() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSafetyGuardPromptIncludesReportContext(t *testing.T) {
	adapter, err := models.Resolve("nvidia/llama-3.1-nemotron-safety-guard-8b-v3")
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}

	report := storage.Report{
		Reason:      "harassment",
		Description: "peer kept threatening me",
	}

	prompt := adapter.BuildPrompt(report, "- I will find you")
	if prompt == "" {
		t.Fatal("BuildPrompt() returned empty prompt")
	}

	if !contains(prompt, "- I will find you") {
		t.Fatalf("buildPrompt() missing peer evidence")
	}

	if contains(prompt, "Report reason: harassment") || contains(prompt, "Reporter description: peer kept threatening me") {
		t.Fatal("BuildPrompt() should not include report metadata for the safety guard model as it causes hallucinations")
	}
}

func TestContentSafetyReasoningModelParsesPromptHarm(t *testing.T) {
	adapter, err := models.Resolve("nvidia/nemotron-content-safety-reasoning-4b")
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}

	assessment, err := adapter.ParseAssessment("<think>\ninternal reasoning\n</think>\nPrompt harm: harmful\nResponse harm: None")
	if err != nil {
		t.Fatalf("ParseAssessment() returned error: %v", err)
	}
	if assessment.UserSafety != "unsafe" {
		t.Fatalf("assessment.UserSafety = %q, want %q", assessment.UserSafety, "unsafe")
	}
}

func TestContentSafetyReasoningPromptIncludesTaxonomyAndEvidence(t *testing.T) {
	adapter, err := models.Resolve("nvidia/nemotron-content-safety-reasoning-4b")
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}

	report := storage.Report{
		Reason:      "harassment",
		Description: "peer kept threatening me",
	}

	prompt := adapter.BuildPrompt(report, "- I will find you")
	for _, want := range []string{"<BEGIN SAFETY TAXONOMY>", "Report reason: harassment", "Prompt harm: <harmful/unharmful>"} {
		if !contains(prompt, want) {
			t.Fatalf("BuildPrompt() missing %q", want)
		}
	}
	if contains(prompt, "Reporter description: peer kept threatening me") {
		t.Fatal("BuildPrompt() should not include reporter description as it biases model classification")
	}
}

func TestResolveRejectsUnknownAutoModerationModel(t *testing.T) {
	if _, err := models.Resolve("nvidia/unknown-model"); err == nil {
		t.Fatal("Resolve() should reject unknown models")
	}
}

func TestMultilingualSafetyGuardUsesJSONFormat(t *testing.T) {
	adapter, err := models.Resolve("nvidia/llama-3.1-nemotron-safety-guard-multilingual-8b-v1")
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}

	assessment, err := adapter.ParseAssessment("{\"User Safety\":\"safe\"}")
	if err != nil {
		t.Fatalf("ParseAssessment() returned error: %v", err)
	}
	if assessment.UserSafety != "safe" {
		t.Fatalf("assessment.UserSafety = %q, want %q", assessment.UserSafety, "safe")
	}
}

func TestNemoGuardContentSafetyUsesJSONFormat(t *testing.T) {
	adapter, err := models.Resolve("nvidia/llama-3.1-nemoguard-8b-content-safety")
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}

	assessment, err := adapter.ParseAssessment("{\"User Safety\":\"unsafe\",\"Safety Categories\":\"PII/Privacy\"}")
	if err != nil {
		t.Fatalf("ParseAssessment() returned error: %v", err)
	}
	if assessment.UserSafety != "unsafe" {
		t.Fatalf("assessment.UserSafety = %q, want %q", assessment.UserSafety, "unsafe")
	}
	if len(assessment.Categories) != 1 || assessment.Categories[0] != "pii/privacy" {
		t.Fatalf("assessment.Categories = %#v", assessment.Categories)
	}
}

func TestLlamaGuard4ParsesPlaintextFormat(t *testing.T) {
	adapter, err := models.Resolve("meta/llama-guard-4-12b")
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}

	assessment, err := adapter.ParseAssessment("unsafe\nS7")
	if err != nil {
		t.Fatalf("ParseAssessment() returned error: %v", err)
	}
	if assessment.UserSafety != "unsafe" {
		t.Fatalf("assessment.UserSafety = %q, want %q", assessment.UserSafety, "unsafe")
	}
	if len(assessment.Categories) != 1 || assessment.Categories[0] != "pii/privacy" {
		t.Fatalf("assessment.Categories = %#v", assessment.Categories)
	}
}

func TestParseBoolWithDefault(t *testing.T) {
	if got := parseBoolWithDefault("", false); got {
		t.Fatal("parseBoolWithDefault(empty, false) should return false")
	}
	if got := parseBoolWithDefault("enabled", false); !got {
		t.Fatal("parseBoolWithDefault(enabled, false) should return true")
	}
	if got := parseBoolWithDefault("disabled", true); got {
		t.Fatal("parseBoolWithDefault(disabled, true) should return false")
	}
}

func TestNormalizeOpenAIBaseURL(t *testing.T) {
	tests := []struct {
		name string
		base string
		want string
	}{
		{
			name: "base already contains v1",
			base: "https://integrate.api.nvidia.com/v1",
			want: "https://integrate.api.nvidia.com/v1",
		},
		{
			name: "base omits v1",
			base: "https://integrate.api.nvidia.com",
			want: "https://integrate.api.nvidia.com/v1",
		},
		{
			name: "full endpoint trims to sdk base url",
			base: "https://integrate.api.nvidia.com/v1/chat/completions",
			want: "https://integrate.api.nvidia.com/v1",
		},
		{
			name: "trailing slash is normalized",
			base: "http://nim.local:8000/",
			want: "http://nim.local:8000/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeOpenAIBaseURL(tt.base)
			if got != tt.want {
				t.Fatalf("normalizeOpenAIBaseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsRetryableAssessmentError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "rate limit is retryable",
			err: &openai.Error{
				StatusCode: http.StatusTooManyRequests,
				Request:    mustRequest(),
				Response:   mustResponse(http.StatusTooManyRequests),
			},
			want: true,
		},
		{
			name: "not found is retryable",
			err: &openai.Error{
				StatusCode: http.StatusNotFound,
				Request:    mustRequest(),
				Response:   mustResponse(http.StatusNotFound),
			},
			want: true,
		},
		{
			name: "bad request is terminal",
			err: &openai.Error{
				StatusCode: http.StatusBadRequest,
				Request:    mustRequest(),
				Response:   mustResponse(http.StatusBadRequest),
			},
			want: false,
		},
		{
			name: "deadline exceeded is retryable",
			err:  context.DeadlineExceeded,
			want: true,
		},
		{
			name: "missing choices is retryable",
			err:  errNIMResponseMissingChoices,
			want: true,
		},
		{
			name: "missing base url is terminal",
			err:  errAutoModerationBaseURLEmpty,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryableAssessmentError(tt.err); got != tt.want {
				t.Fatalf("isRetryableAssessmentError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func mustRequest() *http.Request {
	req, err := http.NewRequest(http.MethodPost, "https://example.com/v1/chat/completions", nil)
	if err != nil {
		panic(err)
	}
	return req
}

func mustResponse(statusCode int) *http.Response {
	return &http.Response{StatusCode: statusCode}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || containsAt(haystack, needle))
}

func containsAt(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
