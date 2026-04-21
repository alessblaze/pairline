package models

import (
	"errors"
	"slices"
	"testing"
)

func TestResolveReturnsKnownAdapter(t *testing.T) {
	adapter, err := Resolve("generic-json")
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}

	if !adapter.Matches("generic-json") {
		t.Fatal("Resolve() returned an adapter that does not match the requested model")
	}
}

func TestResolveRejectsUnknownAdapter(t *testing.T) {
	_, err := Resolve("definitely-unknown-model")
	if !errors.Is(err, ErrUnsupportedModel) {
		t.Fatalf("Resolve() error = %v, want %v", err, ErrUnsupportedModel)
	}
}

func TestSupportedModelIDsIncludesKnownModels(t *testing.T) {
	ids := SupportedModelIDs()

	if !slices.Contains(ids, "meta/llama-guard-4-12b") {
		t.Fatalf("SupportedModelIDs() = %#v, missing llama guard model", ids)
	}
	if !slices.Contains(ids, "nvidia/llama-3.1-nemotron-safety-guard-8b-v3") {
		t.Fatalf("SupportedModelIDs() = %#v, missing safety guard model", ids)
	}
}
