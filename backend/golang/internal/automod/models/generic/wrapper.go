// Pairline - Open Source Video Chat and Matchmaking
// Copyright (C) 2026 Albert Blasczykowski
// Aless Microsystems
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

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

func (adapter) BuildDualMessages(report storage.Report, reportedEvidence, reporterEvidence string) []shared.CoreMessage {
	return []shared.CoreMessage{
		{
			Role:    "user",
			Content: shared.BuildDualJSONSafetyPrompt(report, reportedEvidence, reporterEvidence, true),
		},
	}
}

func (adapter) ParseDualAssessment(raw string) (shared.DualAssessment, error) {
	return shared.ParseDualJSONSafetyAssessment(raw)
}
