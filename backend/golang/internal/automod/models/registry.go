package models

import (
	"errors"
	"fmt"

	metallamaguard412b "github.com/anish/omegle/backend/golang/internal/automod/models/meta_llama_guard_4_12b"
	nvidiallama31nemoguard8bcontentsafety "github.com/anish/omegle/backend/golang/internal/automod/models/nvidia_llama_3_1_nemoguard_8b_content_safety"
	nvidiallama31nemotronsafetyguard8bv3 "github.com/anish/omegle/backend/golang/internal/automod/models/nvidia_llama_3_1_nemotron_safety_guard_8b_v3"
	nvidiallama31nemotronsafetyguardmultilingual8bv1 "github.com/anish/omegle/backend/golang/internal/automod/models/nvidia_llama_3_1_nemotron_safety_guard_multilingual_8b_v1"
	nemotroncontentsafetyreasoning4b "github.com/anish/omegle/backend/golang/internal/automod/models/nvidia_nemotron_content_safety_reasoning_4b"
	"github.com/anish/omegle/backend/golang/internal/automod/models/generic"
	"github.com/anish/omegle/backend/golang/internal/automod/models/shared"
)

var ErrUnsupportedModel = errors.New("unsupported auto moderation model")

var registeredAdapters = []shared.Adapter{
	nvidiallama31nemotronsafetyguard8bv3.New(),
	nvidiallama31nemotronsafetyguardmultilingual8bv1.New(),
	nvidiallama31nemoguard8bcontentsafety.New(),
	nemotroncontentsafetyreasoning4b.New(),
	metallamaguard412b.New(),
	generic.New(),
}

func Resolve(model string) (shared.Adapter, error) {
	for _, adapter := range registeredAdapters {
		if adapter.Matches(model) {
			return adapter, nil
		}
	}

	return nil, fmt.Errorf("%w: %s", ErrUnsupportedModel, model)
}

func SupportedModelIDs() []string {
	models := make([]string, 0, len(registeredAdapters))
	for _, adapter := range registeredAdapters {
		switch typed := adapter.(type) {
		case interface{ ModelID() string }:
			models = append(models, typed.ModelID())
		}
	}
	return models
}
