package bots

import (
	"encoding/json"
	"testing"
)

func TestNormalizeDefinitionAIConfigForAIBots(t *testing.T) {
	raw := json.RawMessage(`{
		"provider": "openai-compatible",
		"api_url": "https://example.com/v1/chat/completions",
		"api_token": "secret-token",
		"model": "gpt-4o-mini",
		"system_prompt": "Be friendly",
		"temperature": 0.4,
		"max_tokens": 250
	}`)

	normalized, err := normalizeDefinitionAIConfig("ai", &raw)
	if err != nil {
		t.Fatalf("normalizeDefinitionAIConfig returned error: %v", err)
	}

	var config AIConfig
	if err := json.Unmarshal(normalized, &config); err != nil {
		t.Fatalf("failed to unmarshal normalized config: %v", err)
	}

	if config.APIURL != "https://example.com/v1/chat/completions" {
		t.Fatalf("unexpected api_url: %q", config.APIURL)
	}
	if config.APIToken != "secret-token" {
		t.Fatalf("unexpected api_token: %q", config.APIToken)
	}
	if config.Model != "gpt-4o-mini" {
		t.Fatalf("unexpected model: %q", config.Model)
	}
	if config.Temperature != 0.4 {
		t.Fatalf("unexpected temperature: %v", config.Temperature)
	}
	if config.MaxTokens != 250 {
		t.Fatalf("unexpected max_tokens: %d", config.MaxTokens)
	}
}

func TestNormalizeDefinitionAIConfigRejectsMissingFields(t *testing.T) {
	raw := json.RawMessage(`{"provider":"openai-compatible"}`)

	_, err := normalizeDefinitionAIConfig("ai", &raw)
	if err == nil {
		t.Fatal("expected error for missing ai config fields")
	}
}

func TestNormalizeDefinitionAIConfigClearsEngagementConfig(t *testing.T) {
	raw := json.RawMessage(`{"api_url":"https://example.com","api_token":"secret","model":"x"}`)

	normalized, err := normalizeDefinitionAIConfig("engagement", &raw)
	if err != nil {
		t.Fatalf("expected nil error for engagement bot config, got %v", err)
	}

	if string(normalized) != "{}" {
		t.Fatalf("expected engagement ai config to be cleared, got %s", string(normalized))
	}
}

func TestSanitizeDefinitionForRoleRedactsAITokenForModerator(t *testing.T) {
	definition := Definition{
		BotType:      "ai",
		AIConfigJSON: json.RawMessage(`{"provider":"openai-compatible","api_url":"https://example.com","api_token":"secret-token","model":"gpt-4o-mini"}`),
	}

	sanitized := sanitizeDefinitionForRole(definition, "moderator")

	var config map[string]any
	if err := json.Unmarshal(sanitized.AIConfigJSON, &config); err != nil {
		t.Fatalf("failed to unmarshal sanitized config: %v", err)
	}

	if _, exists := config["api_token"]; exists {
		t.Fatalf("expected api_token to be redacted for moderator role")
	}
	if got := config["model"]; got != "gpt-4o-mini" {
		t.Fatalf("expected model to remain visible, got %v", got)
	}
}

func TestSanitizeDefinitionForRoleKeepsAITokenForAdmin(t *testing.T) {
	definition := Definition{
		BotType:      "ai",
		AIConfigJSON: json.RawMessage(`{"provider":"openai-compatible","api_url":"https://example.com","api_token":"secret-token","model":"gpt-4o-mini"}`),
	}

	sanitized := sanitizeDefinitionForRole(definition, "admin")

	var config map[string]any
	if err := json.Unmarshal(sanitized.AIConfigJSON, &config); err != nil {
		t.Fatalf("failed to unmarshal admin config: %v", err)
	}

	if got := config["api_token"]; got != "secret-token" {
		t.Fatalf("expected api_token to remain visible for admin, got %v", got)
	}
}
