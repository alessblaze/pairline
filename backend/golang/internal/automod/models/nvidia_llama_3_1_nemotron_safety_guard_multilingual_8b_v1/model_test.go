package multilingualsafetyguard8bv1

import "testing"

func TestAdapterMatchesAndParsesDualAssessment(t *testing.T) {
	adapter := New()

	if !adapter.Matches(modelID) {
		t.Fatalf("Matches() should accept %q", modelID)
	}

	assessment, err := adapter.ParseDualAssessment(`{"User Safety":"safe","Response Safety":"unsafe","Safety Categories":"Harassment"}`)
	if err != nil {
		t.Fatalf("ParseDualAssessment() returned error: %v", err)
	}

	if assessment.ReportedUser.UserSafety != "safe" || assessment.Reporter.UserSafety != "unsafe" {
		t.Fatalf("ParseDualAssessment() = %#v", assessment)
	}
}
