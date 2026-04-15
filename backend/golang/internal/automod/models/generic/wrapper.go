package generic

import (
	"github.com/anish/omegle/backend/golang/internal/automod/models/shared"
	"github.com/anish/omegle/backend/golang/internal/storage"
)

type adapter struct{}

func New() adapter {
	return adapter{}
}

// Matches returns true if the specified model format is "generic-json".
func (adapter) Matches(modelType string) bool {
	return shared.NormalizeModelID(modelType) == "generic-json"
}

// BuildPrompt natively generates the generic zero-shot JSON instruction template.
func (adapter) BuildPrompt(report storage.Report, peerEvidence string) string {
	return shared.BuildJSONSafetyPrompt(report, peerEvidence, true)
}

// ParseAssessment consumes the generic LLM JSON response.
func (adapter) ParseAssessment(raw string) (shared.Assessment, error) {
	return shared.ParseJSONSafetyAssessment(raw)
}
