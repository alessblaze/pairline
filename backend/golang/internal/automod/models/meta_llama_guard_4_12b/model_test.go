package llamaguard412b

import (
	"strings"
	"testing"

	"github.com/anish/omegle/backend/golang/internal/storage"
)

func TestAdapterMatchesAndParsesAssessment(t *testing.T) {
	adapter := New()

	if !adapter.Matches(modelID) {
		t.Fatalf("Matches() should accept %q", modelID)
	}

	assessment, err := adapter.ParseAssessment("unsafe\nS7")
	if err != nil {
		t.Fatalf("ParseAssessment() returned error: %v", err)
	}
	if assessment.UserSafety != "unsafe" {
		t.Fatalf("assessment.UserSafety = %q, want %q", assessment.UserSafety, "unsafe")
	}
}

func TestBuildPromptSplitsEvidenceIntoUserTurns(t *testing.T) {
	adapter := New()
	prompt := adapter.BuildPrompt(storage.Report{}, "- first line\n- second line")

	if !strings.Contains(prompt, "User: first line") || !strings.Contains(prompt, "User: second line") {
		t.Fatalf("BuildPrompt() = %q", prompt)
	}
}
