package llamaguard412b

import (
	"strings"

	"github.com/anish/omegle/backend/golang/internal/automod/models/shared"
	"github.com/anish/omegle/backend/golang/internal/automod/taxonomy"
	"github.com/anish/omegle/backend/golang/internal/storage"
)

const modelID = "meta/llama-guard-4-12b"

// mlgTaxonomy is the official MLCommons hazard taxonomy that Llama Guard 4
// was fine-tuned on. Using any other labels causes the model to ignore
// categories it was not trained to recognise.
const mlgTaxonomy = `S1: Violent Crimes
S2: Non-Violent Crimes
S3: Sex-Related Crimes
S4: Child Sexual Exploitation
S5: Defamation
S6: Specialized Advice
S7: Privacy
S8: Intellectual Property
S9: Indiscriminate Weapons
S10: Hate
S11: Suicide & Self-Harm
S12: Sexual Content
S13: Elections
S14: Code Interpreter Abuse
`

// llamaGuardCategoryMap translates the MLCommons codes returned by the model
// into the canonical categories used by the rest of the moderation system.
var llamaGuardCategoryMap = map[string]string{
	"s1":  "violence",
	"s2":  "illegal activity",
	"s3":  "sexual",
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

	var b strings.Builder
	b.WriteString("Task: Check if there is unsafe content in 'User' messages in conversations according to our safety policy with the below categories.\n\n")
	b.WriteString("<BEGIN UNSAFE CONTENT CATEGORIES>\n")
	b.WriteString(mlgTaxonomy)
	b.WriteString("<END UNSAFE CONTENT CATEGORIES>\n\n")
	b.WriteString("<BEGIN CONVERSATION>\n\n")
	if content != "" {
		// Each line of evidence becomes a separate User turn.
		for _, line := range strings.Split(content, "\n") {
			text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
			if text == "" {
				continue
			}
			b.WriteString("User: ")
			b.WriteString(text)
			b.WriteString("\n\n")
		}
	}
	b.WriteString("<END CONVERSATION>\n\n")
	b.WriteString("Provide your safety assessment for User in the above conversation:\n")
	b.WriteString("- First line must read 'safe' or 'unsafe'.\n")
	b.WriteString("- If unsafe, a second line must include a comma-separated list of violated categories.\n")
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
