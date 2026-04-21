package nemoguardcontentsafety8b

import "testing"

func TestAdapterMatchesAndParsesAssessment(t *testing.T) {
	adapter := New()

	if !adapter.Matches(modelID) {
		t.Fatalf("Matches() should accept %q", modelID)
	}

	assessment, err := adapter.ParseAssessment(`{"User Safety":"unsafe","Safety Categories":"PII/Privacy"}`)
	if err != nil {
		t.Fatalf("ParseAssessment() returned error: %v", err)
	}

	if assessment.UserSafety != "unsafe" {
		t.Fatalf("assessment.UserSafety = %q, want %q", assessment.UserSafety, "unsafe")
	}
}
