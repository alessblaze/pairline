package llamaguard412b

import (
	"strings"

	"github.com/anish/omegle/backend/golang/internal/automod/models/shared"
	"github.com/anish/omegle/backend/golang/internal/automod/taxonomy"
	"github.com/anish/omegle/backend/golang/internal/storage"
)

const modelID = "meta/llama-guard-4-12b"

const mlgTaxonomy = `S1: Illegal activity
S2: Illegal activity
S3: Illegal activity
S4: Sexual (minor)
S5: Other
S6: Unauthorized advice
S7: PII / Privacy
S8: Copyright / Trademark / Plagiarism
S9: Guns and illegal weapons
S10: Hate / identity hate
S11: Suicide and self harm
S12: Sexual
S13: Political / misinformation / conspiracy
S14: Malware
`

var llamaGuardCategoryMap = map[string]string{
	"s1":  "illegal activity",
	"s2":  "illegal activity",
	"s3":  "illegal activity",
	"s4":  "sexual (minor)",
	"s5":  "other",
	"s6":  "unauthorized advice",
	"s7":  "pii/privacy",
	"s8":  "copyright/trademark/plagiarism",
	"s9":  "guns and illegal weapons",
	"s10": "hate/identity hate",
	"s11": "suicide and self harm",
	"s12": "sexual",
	"s13": "political/misinformation/conspiracy",
	"s14": "malware",
}

type adapter struct{}

func New() adapter {
	return adapter{}
}

func (adapter) ModelID() string {
	return modelID
}

func (adapter) Matches(model string) bool {
	return shared.NormalizeModelID(model) == modelID
}

func (adapter) BuildPrompt(report storage.Report, peerEvidence string) string {
	content := strings.TrimSpace(peerEvidence)
	if content == "" {
		content = shared.SanitizePromptText(report.Description)
	}

	var b strings.Builder
	b.WriteString("Classify only the reported user's content using the safety taxonomy below. ")
	b.WriteString("If the content is unsafe, return the violated category codes from the taxonomy. ")
	b.WriteString("Ignore the reporter's chosen reason and classify only the quoted content.\n")
	b.WriteString("<BEGIN SAFETY TAXONOMY>\n")
	b.WriteString(mlgTaxonomy)
	b.WriteString("<END SAFETY TAXONOMY>\n")
	b.WriteString("<BEGIN REPORTED USER CONTENT>\n")
	if content != "" {
		b.WriteString(content)
		b.WriteString("\n")
	}
	b.WriteString("<END REPORTED USER CONTENT>\n")
	b.WriteString("Output only:\n")
	b.WriteString("safe\n")
	b.WriteString("or\n")
	b.WriteString("unsafe\n")
	b.WriteString("<comma-separated category codes>\n")
	return b.String()
}

func (adapter) ParseAssessment(raw string) (shared.Assessment, error) {
	assessment, err := shared.ParsePlaintextSafetyAssessment(raw)
	if err != nil {
		return shared.Assessment{}, err
	}
	assessment.Categories = localizeLlamaGuardCategories(assessment.Categories)
	return assessment, nil
}

func localizeLlamaGuardCategories(categories []string) []string {
	if len(categories) == 0 {
		return nil
	}

	local := make([]string, 0, len(categories))
	for _, category := range categories {
		mapped, ok := llamaGuardCategoryMap[strings.ToLower(strings.TrimSpace(category))]
		if ok {
			local = append(local, mapped)
			continue
		}
		local = append(local, category)
	}
	return taxonomy.CanonicalizeMany(local)
}
