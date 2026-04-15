package taxonomy

import "testing"

func TestCanonicalizeMapsCodesAndAliases(t *testing.T) {
	tests := map[string]string{
		"S9":                    "pii/privacy",
		"Privacy":               "pii/privacy",
		"Intellectual Property": "copyright/trademark/plagiarism",
		"Specialized Advice":    "unauthorized advice",
		"Violent Crimes":        "illegal activity",
		"Sex-Related Crimes":    "illegal activity",
		"Hate":                  "hate/identity hate",
		"Suicide & Self-Harm":   "suicide and self harm",
		"sexual harassment":     "harassment",
	}

	for input, want := range tests {
		if got := Canonicalize(input); got != want {
			t.Fatalf("Canonicalize(%q) = %q, want %q", input, got, want)
		}
	}
}
