package safetyguard8bv3

import "testing"

func TestAdapterMatchesAndParsesDualAssessment(t *testing.T) {
	adapter := New()

	if !adapter.Matches(modelID) {
		t.Fatalf("Matches() should accept %q", modelID)
	}

	assessment, err := adapter.ParseDualAssessment(`{"User Safety":"unsafe","Response Safety":"safe","Safety Categories":"Harassment"}`)
	if err != nil {
		t.Fatalf("ParseDualAssessment() returned error: %v", err)
	}

	if assessment.ReportedUser.UserSafety != "unsafe" || assessment.Reporter.UserSafety != "safe" {
		t.Fatalf("ParseDualAssessment() = %#v", assessment)
	}
}
