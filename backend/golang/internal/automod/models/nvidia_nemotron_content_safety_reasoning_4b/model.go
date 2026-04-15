package contentsafetyreasoning4b

import (
	"errors"
	"regexp"
	"strings"

	"github.com/anish/omegle/backend/golang/internal/automod/models/shared"
	"github.com/anish/omegle/backend/golang/internal/automod/taxonomy"
	"github.com/anish/omegle/backend/golang/internal/storage"
)

const modelID = "nvidia/nemotron-content-safety-reasoning-4b"

var (
	errMissingPromptHarm = errors.New("model response missing prompt harm classification")
	promptHarmPattern    = regexp.MustCompile(`(?im)^\s*Prompt harm:\s*(harmful|unharmful)\s*$`)
)

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
	var b strings.Builder
	b.WriteString("You are a classifier for analyzing a chat abuse report. ")
	b.WriteString("Given the reported user's messages and the reporter's evidence, determine whether the reported user's content violates the safety taxonomy below.\n")
	b.WriteString("<BEGIN SAFETY TAXONOMY>\n")
	b.WriteString(taxonomy.Render())
	b.WriteString("<END SAFETY TAXONOMY>\n")
	b.WriteString("Reported user:\n")
	if peerEvidence != "" {
		b.WriteString(peerEvidence)
		b.WriteString("\n")
	} else {
		b.WriteString("- None provided\n")
	}
	b.WriteString("Reporter evidence:\n")
	b.WriteString("Report reason: " + shared.SanitizePromptText(report.Reason) + "\n")
	if description := strings.TrimSpace(report.Description); description != "" {
		b.WriteString("Reporter description: " + shared.SanitizePromptText(description) + "\n")
	}
	b.WriteString("AI assistant:\nNone\n")
	b.WriteString("Use the taxonomy to determine if the reported user's content is harmful. Output your answer in the following format:\n")
	b.WriteString("Prompt harm: <harmful/unharmful>\n")
	b.WriteString("Response harm: None\n")
	b.WriteString("Only output the answer in the format specified above. /no_think\n")
	return b.String()
}

func (adapter) ParseAssessment(raw string) (shared.Assessment, error) {
	matches := promptHarmPattern.FindStringSubmatch(raw)
	if len(matches) < 2 {
		return shared.Assessment{}, errMissingPromptHarm
	}

	switch strings.ToLower(strings.TrimSpace(matches[1])) {
	case "harmful":
		return shared.Assessment{UserSafety: "unsafe"}, nil
	case "unharmful":
		return shared.Assessment{UserSafety: "safe"}, nil
	default:
		return shared.Assessment{}, errMissingPromptHarm
	}
}
