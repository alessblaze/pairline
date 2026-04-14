package automod

import (
	"testing"

	"github.com/anish/omegle/backend/golang/internal/storage"
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

func TestParseAssessmentAcceptsJSONWrappedInText(t *testing.T) {
	assessment, err := parseAssessment("Output JSON:\n{\"User Safety\":\"unsafe\",\"Safety Categories\":\"Harassment, Profanity\"}")
	if err != nil {
		t.Fatalf("parseAssessment returned error: %v", err)
	}
	if assessment.UserSafety != "unsafe" {
		t.Fatalf("assessment.UserSafety = %q, want %q", assessment.UserSafety, "unsafe")
	}
	if len(assessment.Categories) != 2 || assessment.Categories[0] != "harassment" || assessment.Categories[1] != "profanity" {
		t.Fatalf("assessment.Categories = %#v", assessment.Categories)
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

func TestBuildPromptIncludesReportContext(t *testing.T) {
	report := storage.Report{
		Reason:      "harassment",
		Description: "peer kept threatening me",
	}

	prompt := buildPrompt(report, "- I will find you")
	if prompt == "" {
		t.Fatal("buildPrompt() returned empty prompt")
	}
	for _, want := range []string{"Report reason: harassment", "Reporter description: peer kept threatening me", "- I will find you"} {
		if !contains(prompt, want) {
			t.Fatalf("buildPrompt() missing %q", want)
		}
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
