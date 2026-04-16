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
	responseHarmPattern  = regexp.MustCompile(`(?im)^\s*Response harm:\s*(harmful|unharmful)\s*$`)
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
	b.WriteString("Human user:\n")
	if peerEvidence != "" {
		b.WriteString(peerEvidence)
		b.WriteString("\n")
	} else {
		b.WriteString("- None provided\n")
	}
	b.WriteString("AI assistant:\n")
	b.WriteString("Report reason: " + shared.SanitizePromptText(report.Reason) + "\n")
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

func (a adapter) BuildDualMessages(report storage.Report, reportedEvidence, reporterEvidence string) []shared.CoreMessage {
	return []shared.CoreMessage{
		{
			Role:    "user",
			Content: a.buildDualPrompt(report, reportedEvidence, reporterEvidence),
		},
	}
}

func (adapter) buildDualPrompt(_ storage.Report, reportedEvidence, reporterEvidence string) string {
	var b strings.Builder
	b.WriteString("You are a classifier for analyzing interactions between humans and LLMs. ")
	b.WriteString("Given a request from a Human user and a response from an AI assistant, determine 1) whether the human user's context is harmful, and 2) whether the AI assistant's context is harmful.\n")
	b.WriteString("<BEGIN SAFETY TAXONOMY>\n")
	b.WriteString(taxonomy.Render())
	b.WriteString("<END SAFETY TAXONOMY>\n")
	b.WriteString("Human user:\n")
	if reportedEvidence != "" {
		b.WriteString(reportedEvidence)
		b.WriteString("\n")
	} else {
		b.WriteString("- None provided\n")
	}
	b.WriteString("AI assistant:\n")
	if reporterEvidence != "" {
		b.WriteString(reporterEvidence)
		b.WriteString("\n")
	} else {
		b.WriteString("- None provided\n")
	}
	b.WriteString("Use the taxonomy to determine if the prompt and response fall into any of the harmful categories. Output your answer in the following format:\n")
	b.WriteString("Prompt harm: <harmful/unharmful>\n")
	b.WriteString("Response harm: <harmful/unharmful>\n")
	b.WriteString("Only output the answer in the format specified above after thinking. /think\n")
	return b.String()
}

func (adapter) ParseDualAssessment(raw string) (shared.DualAssessment, error) {
	promptMatches := promptHarmPattern.FindStringSubmatch(raw)
	if len(promptMatches) < 2 {
		return shared.DualAssessment{}, errMissingPromptHarm
	}

	responseMatches := responseHarmPattern.FindStringSubmatch(raw)
	if len(responseMatches) < 2 {
		return shared.DualAssessment{}, errors.New("model response missing response harm classification")
	}

	var dualAssessment shared.DualAssessment

	switch strings.ToLower(strings.TrimSpace(promptMatches[1])) {
	case "harmful":
		dualAssessment.ReportedUser = shared.Assessment{UserSafety: "unsafe"}
	case "unharmful":
		dualAssessment.ReportedUser = shared.Assessment{UserSafety: "safe"}
	default:
		return shared.DualAssessment{}, errMissingPromptHarm
	}

	switch strings.ToLower(strings.TrimSpace(responseMatches[1])) {
	case "harmful":
		dualAssessment.Reporter = shared.Assessment{UserSafety: "unsafe"}
	case "unharmful":
		dualAssessment.Reporter = shared.Assessment{UserSafety: "safe"}
	default:
		return shared.DualAssessment{}, errors.New("unexpected response harm value")
	}

	return dualAssessment, nil
}
