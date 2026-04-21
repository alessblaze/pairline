package shared

import (
	"errors"
	"testing"
)

func TestExtractJSONObject(t *testing.T) {
	got, err := ExtractJSONObject("prefix {\"User Safety\":\"safe\"} suffix")
	if err != nil {
		t.Fatalf("ExtractJSONObject() returned error: %v", err)
	}

	if got != "{\"User Safety\":\"safe\"}" {
		t.Fatalf("ExtractJSONObject() = %q, want %q", got, "{\"User Safety\":\"safe\"}")
	}

	if _, err := ExtractJSONObject("no json here"); !errors.Is(err, ErrModelResponseMissingJSON) {
		t.Fatalf("ExtractJSONObject(no json) error = %v, want %v", err, ErrModelResponseMissingJSON)
	}
}

func TestParseNativeDualAssessment(t *testing.T) {
	assessment, err := ParseNativeDualAssessment(`{"User Safety":"unsafe","Response Safety":"safe","Safety Categories":"Harassment"}`)
	if err != nil {
		t.Fatalf("ParseNativeDualAssessment() returned error: %v", err)
	}

	if assessment.ReportedUser.UserSafety != "unsafe" {
		t.Fatalf("reported safety = %q, want %q", assessment.ReportedUser.UserSafety, "unsafe")
	}
	if assessment.Reporter.UserSafety != "safe" {
		t.Fatalf("reporter safety = %q, want %q", assessment.Reporter.UserSafety, "safe")
	}
	if len(assessment.ReportedUser.Categories) != 1 || assessment.ReportedUser.Categories[0] != "harassment" {
		t.Fatalf("reported categories = %#v", assessment.ReportedUser.Categories)
	}
}

func TestParsePlaintextSafetyAssessment(t *testing.T) {
	assessment, err := ParsePlaintextSafetyAssessment("```unsafe\nS7\n```")
	if err != nil {
		t.Fatalf("ParsePlaintextSafetyAssessment() returned error: %v", err)
	}

	if assessment.UserSafety != "unsafe" {
		t.Fatalf("assessment.UserSafety = %q, want %q", assessment.UserSafety, "unsafe")
	}
	if len(assessment.Categories) != 1 || assessment.Categories[0] != "s7" {
		t.Fatalf("assessment.Categories = %#v", assessment.Categories)
	}
}

func TestBuildNativeDualMessagesSuppliesFallbackText(t *testing.T) {
	messages := BuildNativeDualMessages("", "")
	if len(messages) != 2 {
		t.Fatalf("BuildNativeDualMessages() length = %d, want %d", len(messages), 2)
	}

	if messages[0].Content != "No messages provided" || messages[1].Content != "No messages provided" {
		t.Fatalf("BuildNativeDualMessages() = %#v", messages)
	}
}
