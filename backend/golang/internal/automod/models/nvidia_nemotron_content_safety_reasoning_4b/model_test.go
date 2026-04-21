package contentsafetyreasoning4b

import (
	"strings"
	"testing"

	"github.com/anish/omegle/backend/golang/internal/storage"
)

func TestAdapterMatchesAndParsesDualAssessment(t *testing.T) {
	adapter := New()

	if !adapter.Matches(modelID) {
		t.Fatalf("Matches() should accept %q", modelID)
	}

	assessment, err := adapter.ParseDualAssessment("Prompt harm: harmful\nResponse harm: unharmful")
	if err != nil {
		t.Fatalf("ParseDualAssessment() returned error: %v", err)
	}

	if assessment.ReportedUser.UserSafety != "unsafe" || assessment.Reporter.UserSafety != "safe" {
		t.Fatalf("ParseDualAssessment() = %#v", assessment)
	}
}

func TestBuildDualMessagesIncludesEvidence(t *testing.T) {
	adapter := New()
	messages := adapter.BuildDualMessages(storage.Report{}, "- reported", "- reporter")

	if len(messages) != 1 {
		t.Fatalf("BuildDualMessages() length = %d, want %d", len(messages), 1)
	}
	if !strings.Contains(messages[0].Content, "- reported") || !strings.Contains(messages[0].Content, "- reporter") {
		t.Fatalf("BuildDualMessages() = %#v", messages)
	}
}
