package generic

import (
	"testing"

	"github.com/anish/omegle/backend/golang/internal/storage"
)

func TestGenericAdapterMatchesAndParses(t *testing.T) {
	adapter := New()

	if !adapter.Matches("generic-json") {
		t.Fatal("Matches() should accept generic-json")
	}

	assessment, err := adapter.ParseAssessment(`{"User Safety":"safe"}`)
	if err != nil {
		t.Fatalf("ParseAssessment() returned error: %v", err)
	}
	if assessment.UserSafety != "safe" {
		t.Fatalf("assessment.UserSafety = %q, want %q", assessment.UserSafety, "safe")
	}
}

func TestGenericAdapterBuildsDualMessages(t *testing.T) {
	adapter := New()
	messages := adapter.BuildDualMessages(storage.Report{Reason: "abuse"}, "- reported", "- reporter")

	if len(messages) != 1 {
		t.Fatalf("BuildDualMessages() length = %d, want %d", len(messages), 1)
	}
	if messages[0].Role != "user" || messages[0].Content == "" {
		t.Fatalf("BuildDualMessages() = %#v", messages)
	}
}
