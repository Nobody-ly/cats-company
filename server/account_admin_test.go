package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openchat/openchat/server/store/types"
)

func TestAccountAdminPageAllowsTunnelAddress(t *testing.T) {
	handler := NewAccountAdminHandler(accountTestUserLookup{users: map[int64]*types.User{}}, nil)

	req := httptest.NewRequest(http.MethodGet, "/local/account-admin", nil)
	req.RemoteAddr = "172.18.0.1:40200"
	rec := httptest.NewRecorder()

	handler.HandlePage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("unexpected content-type %q", got)
	}
}

func TestAccountAdminRejectsPublicAddress(t *testing.T) {
	handler := NewAccountAdminHandler(accountTestUserLookup{users: map[int64]*types.User{}}, nil)

	req := httptest.NewRequest(http.MethodGet, "/local/account-admin", nil)
	req.RemoteAddr = "203.0.113.20:40200"
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	rec := httptest.NewRecorder()

	handler.HandlePage(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAccountAdminUserLookup(t *testing.T) {
	verifier, err := NewEnvAccountServiceVerifier("relay=service-secret")
	if err != nil {
		t.Fatalf("NewEnvAccountServiceVerifier: %v", err)
	}
	handler := NewAccountAdminHandler(accountTestUserLookup{users: map[int64]*types.User{
		9: {
			ID:          9,
			Username:    "carol",
			Email:       "carol@example.com",
			DisplayName: "Carol",
			AccountType: types.AccountHuman,
			State:       0,
		},
	}}, verifier)

	req := httptest.NewRequest(http.MethodGet, "/local/account-admin/users?uid=9", nil)
	req.RemoteAddr = "127.0.0.1:40200"
	rec := httptest.NewRecorder()

	handler.HandleUserLookup(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		User struct {
			UID      int64  `json:"uid"`
			Username string `json:"username"`
		} `json:"user"`
		AccountCenter struct {
			ServiceTokensConfigured bool `json:"service_tokens_configured"`
		} `json:"account_center"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.User.UID != 9 || body.User.Username != "carol" {
		t.Fatalf("unexpected user payload: %+v", body.User)
	}
	if !body.AccountCenter.ServiceTokensConfigured {
		t.Fatal("expected configured service token flag")
	}
}
