package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPRateLimiterUsesRemoteAddrForDirectRequests(t *testing.T) {
	limiter := NewHTTPRateLimiter()
	handler := limiter.LimitIP(HTTPRateLimitConfig{
		Name: "direct", Limit: 1, Window: time.Hour, Burst: 1,
	})(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	req1 := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req1.RemoteAddr = "203.0.113.10:12345"
	req1.Header.Set("X-Forwarded-For", "198.51.100.1")
	resp1 := httptest.NewRecorder()
	handler(resp1, req1)
	if resp1.Code != http.StatusNoContent {
		t.Fatalf("first direct request status = %d, want %d", resp1.Code, http.StatusNoContent)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req2.RemoteAddr = "203.0.113.10:54321"
	req2.Header.Set("X-Forwarded-For", "198.51.100.2")
	resp2 := httptest.NewRecorder()
	handler(resp2, req2)
	if resp2.Code != http.StatusTooManyRequests {
		t.Fatalf("second direct request status = %d, want %d", resp2.Code, http.StatusTooManyRequests)
	}
}

func TestHTTPRateLimiterTrustsForwardedForFromLocalProxy(t *testing.T) {
	limiter := NewHTTPRateLimiter()
	handler := limiter.LimitIP(HTTPRateLimitConfig{
		Name: "proxy", Limit: 1, Window: time.Hour, Burst: 1,
	})(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	req1 := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req1.RemoteAddr = "127.0.0.1:12345"
	req1.Header.Set("X-Forwarded-For", "198.51.100.1")
	resp1 := httptest.NewRecorder()
	handler(resp1, req1)
	if resp1.Code != http.StatusNoContent {
		t.Fatalf("first proxied request status = %d, want %d", resp1.Code, http.StatusNoContent)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req2.RemoteAddr = "127.0.0.1:12345"
	req2.Header.Set("X-Forwarded-For", "198.51.100.2")
	resp2 := httptest.NewRecorder()
	handler(resp2, req2)
	if resp2.Code != http.StatusNoContent {
		t.Fatalf("different forwarded IP status = %d, want %d", resp2.Code, http.StatusNoContent)
	}

	req3 := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req3.RemoteAddr = "127.0.0.1:12345"
	req3.Header.Set("X-Forwarded-For", "198.51.100.1")
	resp3 := httptest.NewRecorder()
	handler(resp3, req3)
	if resp3.Code != http.StatusTooManyRequests {
		t.Fatalf("repeated forwarded IP status = %d, want %d", resp3.Code, http.StatusTooManyRequests)
	}
}

func TestHTTPRateLimiterJSONFieldRestoresRequestBody(t *testing.T) {
	limiter := NewHTTPRateLimiter()
	handler := limiter.LimitJSONField(HTTPRateLimitConfig{
		Name: "account", Limit: 1, Window: time.Hour, Burst: 1,
	}, "account")(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("handler could not decode restored body: %v", err)
		}
		if payload["account"] != "demo@example.com" {
			t.Fatalf("account = %q, want demo@example.com", payload["account"])
		}
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"account":"demo@example.com"}`))
	resp := httptest.NewRecorder()
	handler(resp, req)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusNoContent)
	}
}
