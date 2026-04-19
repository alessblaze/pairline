package bots

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const defaultAIProvider = "openai-compatible"

type AIConfig struct {
	Enabled      bool    `json:"enabled"`
	Provider     string  `json:"provider"`
	APIURL       string  `json:"api_url"`
	APIToken     string  `json:"api_token"`
	Model        string  `json:"model"`
	SystemPrompt string  `json:"system_prompt,omitempty"`
	Temperature  float64 `json:"temperature,omitempty"`
	MaxTokens    int     `json:"max_tokens,omitempty"`
}

func normalizeDefinitionAIConfig(botType string, raw *json.RawMessage) (json.RawMessage, error) {
	if strings.ToLower(strings.TrimSpace(botType)) != "ai" {
		return mustMarshalJSON(map[string]any{}), nil
	}

	config, err := parseAIConfig(raw)
	if err != nil {
		return nil, err
	}

	return mustMarshalJSON(config), nil
}

func sanitizeDefinitionForRole(definition Definition, role string) Definition {
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "admin" || role == "root" {
		return definition
	}

	definition.AIConfigJSON = sanitizeAIConfigJSON(definition.AIConfigJSON)
	return definition
}

func sanitizeAIConfigJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return mustMarshalJSON(map[string]any{})
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return mustMarshalJSON(map[string]any{})
	}

	delete(decoded, "api_token")
	delete(decoded, "api_key")
	delete(decoded, "token")

	return mustMarshalJSON(decoded)
}

func parseAIConfig(raw *json.RawMessage) (AIConfig, error) {
	if raw == nil || len(*raw) == 0 || string(*raw) == "null" {
		return AIConfig{}, errors.New("ai_config_json is required for ai bots")
	}

	var decoded map[string]any
	if err := json.Unmarshal(*raw, &decoded); err != nil {
		return AIConfig{}, fmt.Errorf("ai_config_json must be a valid JSON object")
	}

	endpoint := firstNonEmptyString(
		stringValue(decoded["api_url"]),
		stringValue(decoded["endpoint"]),
		stringValue(decoded["base_url"]),
	)
	if endpoint == "" {
		return AIConfig{}, errors.New("ai_config_json.api_url is required for ai bots")
	}
	if len(endpoint) > 2048 {
		return AIConfig{}, errors.New("ai_config_json.api_url must be at most 2048 characters")
	}

	token := firstNonEmptyString(
		stringValue(decoded["api_token"]),
		stringValue(decoded["token"]),
		stringValue(decoded["api_key"]),
	)
	if token == "" {
		return AIConfig{}, errors.New("ai_config_json.api_token is required for ai bots")
	}
	if len(token) > 4096 {
		return AIConfig{}, errors.New("ai_config_json.api_token must be at most 4096 characters")
	}

	model := firstNonEmptyString(
		stringValue(decoded["model"]),
		stringValue(decoded["model_name"]),
	)
	if model == "" {
		return AIConfig{}, errors.New("ai_config_json.model is required for ai bots")
	}
	if len(model) > 255 {
		return AIConfig{}, errors.New("ai_config_json.model must be at most 255 characters")
	}

	provider := strings.TrimSpace(stringValue(decoded["provider"]))
	if provider == "" {
		provider = defaultAIProvider
	}
	if len(provider) > 120 {
		return AIConfig{}, errors.New("ai_config_json.provider must be at most 120 characters")
	}

	systemPrompt := strings.TrimSpace(stringValue(decoded["system_prompt"]))
	if len(systemPrompt) > 8_000 {
		return AIConfig{}, errors.New("ai_config_json.system_prompt must be at most 8000 characters")
	}

	temperature, err := optionalFloat(decoded["temperature"])
	if err != nil {
		return AIConfig{}, errors.New("ai_config_json.temperature must be a number between 0 and 2")
	}
	if temperature < 0 || temperature > 2 {
		return AIConfig{}, errors.New("ai_config_json.temperature must be between 0 and 2")
	}

	maxTokens, err := optionalInt(decoded["max_tokens"])
	if err != nil {
		return AIConfig{}, errors.New("ai_config_json.max_tokens must be an integer between 1 and 16000")
	}
	if maxTokens < 0 || maxTokens > 16_000 {
		return AIConfig{}, errors.New("ai_config_json.max_tokens must be between 1 and 16000")
	}

	config := AIConfig{
		Enabled:      optionalBool(decoded["enabled"], true),
		Provider:     provider,
		APIURL:       endpoint,
		APIToken:     token,
		Model:        model,
		SystemPrompt: systemPrompt,
		Temperature:  temperature,
		MaxTokens:    maxTokens,
	}

	return config, nil
}

func stringValue(value any) string {
	str, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(str)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func optionalBool(value any, defaultValue bool) bool {
	parsed, ok := value.(bool)
	if !ok {
		return defaultValue
	}
	return parsed
}

func optionalFloat(value any) (float64, error) {
	if value == nil {
		return 0, nil
	}

	number, ok := value.(float64)
	if !ok {
		return 0, errors.New("invalid float")
	}

	return number, nil
}

func optionalInt(value any) (int, error) {
	if value == nil {
		return 0, nil
	}

	number, ok := value.(float64)
	if !ok {
		return 0, errors.New("invalid int")
	}

	if number != float64(int(number)) {
		return 0, errors.New("invalid int")
	}

	return int(number), nil
}
