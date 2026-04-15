package multilingualsafetyguard8bv1

import (
	"github.com/anish/omegle/backend/golang/internal/automod/models/shared"
	"github.com/anish/omegle/backend/golang/internal/storage"
)

const modelID = "nvidia/llama-3.1-nemotron-safety-guard-multilingual-8b-v1"

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
	return shared.BuildJSONSafetyPrompt(report, peerEvidence, false)
}

func (adapter) ParseAssessment(raw string) (shared.Assessment, error) {
	return shared.ParseJSONSafetyAssessment(raw)
}


