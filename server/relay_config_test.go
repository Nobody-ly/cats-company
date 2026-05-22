package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRelayConfigDefaults(t *testing.T) {
	handler := NewRelayConfigHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/relay/config", nil)
	rec := httptest.NewRecorder()

	handler.HandleConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var body relayConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.BaseURL != defaultRelayBaseURL {
		t.Fatalf("unexpected base url: %s", body.BaseURL)
	}
	if body.DefaultModel == "" {
		t.Fatal("expected default model")
	}
	if len(body.Endpoints) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(body.Endpoints))
	}
	if body.Endpoints[0].BaseURL != defaultRelayBaseURL+"/v1" {
		t.Fatalf("unexpected openai endpoint: %s", body.Endpoints[0].BaseURL)
	}
	if body.Endpoints[1].BaseURL != defaultRelayBaseURL+"/anthropic" {
		t.Fatalf("unexpected anthropic endpoint: %s", body.Endpoints[1].BaseURL)
	}
}

func TestRelayConfigUsesEnvironmentOverrides(t *testing.T) {
	t.Setenv("CATS_RELAY_PUBLIC_BASE_URL", "https://relay.example.com/")
	t.Setenv("CATS_RELAY_OPENAI_BASE_URL", "https://openai.example.com/v1/")
	t.Setenv("CATS_RELAY_ANTHROPIC_BASE_URL", "https://anthropic.example.com/anthropic/")
	t.Setenv("CATS_RELAY_DEFAULT_MODEL", "custom-model")

	handler := NewRelayConfigHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/relay/config", nil)
	rec := httptest.NewRecorder()

	handler.HandleConfig(rec, req)

	var body relayConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.BaseURL != "https://relay.example.com" {
		t.Fatalf("unexpected base url: %s", body.BaseURL)
	}
	if body.DefaultModel != "custom-model" {
		t.Fatalf("unexpected model: %s", body.DefaultModel)
	}
	if body.Endpoints[0].BaseURL != "https://openai.example.com/v1" {
		t.Fatalf("unexpected openai endpoint: %s", body.Endpoints[0].BaseURL)
	}
	if body.Endpoints[1].BaseURL != "https://anthropic.example.com/anthropic" {
		t.Fatalf("unexpected anthropic endpoint: %s", body.Endpoints[1].BaseURL)
	}
}

func TestRelayConfigRejectsUnsupportedMethod(t *testing.T) {
	handler := NewRelayConfigHandler()
	req := httptest.NewRequest(http.MethodPost, "/api/relay/config", nil)
	rec := httptest.NewRecorder()

	handler.HandleConfig(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
