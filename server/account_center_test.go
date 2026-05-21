package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openchat/openchat/server/store/types"
)

type accountTestUserLookup struct {
	users map[int64]*types.User
	err   error
}

func withTestUID(req *http.Request, uid int64) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), uidKey, uid))
}

func (s accountTestUserLookup) GetUser(id int64) (*types.User, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.users[id], nil
}

func TestEnvAccountServiceVerifierSupportsPlainAndHashTokens(t *testing.T) {
	sum := sha256.Sum256([]byte("hashed-secret"))
	verifier, err := NewEnvAccountServiceVerifier("relay=plain-secret;worker=sha256:" + hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatalf("NewEnvAccountServiceVerifier: %v", err)
	}

	if service, ok := verifier.Verify("plain-secret"); !ok || service.Slug != "relay" {
		t.Fatalf("plain token not verified: service=%+v ok=%v", service, ok)
	}
	if service, ok := verifier.Verify("hashed-secret"); !ok || service.Slug != "worker" {
		t.Fatalf("hashed token not verified: service=%+v ok=%v", service, ok)
	}
	if _, ok := verifier.Verify("wrong-secret"); ok {
		t.Fatal("unexpected verification success for wrong token")
	}
}

func TestAccountServiceVerifierSupportsDatabaseTokens(t *testing.T) {
	store := newAccountTestAuthServiceStore()
	token, prefix, hash, err := GenerateAccountServiceToken()
	if err != nil {
		t.Fatalf("GenerateAccountServiceToken: %v", err)
	}
	if _, err := store.CreateAuthService(&types.AuthService{
		Slug:        "cats-relay",
		Name:        "Cats Relay",
		TokenPrefix: prefix,
		TokenHash:   hash,
		Scopes:      []string{"account.introspect"},
		State:       0,
	}); err != nil {
		t.Fatalf("CreateAuthService: %v", err)
	}

	verifier, err := NewAccountServiceVerifier("", store)
	if err != nil {
		t.Fatalf("NewAccountServiceVerifier: %v", err)
	}
	service, ok := verifier.Verify(token)
	if !ok {
		t.Fatal("expected database token to verify")
	}
	if service.Slug != "cats-relay" || service.Source != "db" {
		t.Fatalf("unexpected service: %+v", service)
	}
}

func TestAccountServiceVerifierRejectsRevokedDatabaseTokens(t *testing.T) {
	store := newAccountTestAuthServiceStore()
	token, prefix, hash, err := GenerateAccountServiceToken()
	if err != nil {
		t.Fatalf("GenerateAccountServiceToken: %v", err)
	}
	id, err := store.CreateAuthService(&types.AuthService{
		Slug:        "cats-relay",
		Name:        "Cats Relay",
		TokenPrefix: prefix,
		TokenHash:   hash,
		State:       0,
	})
	if err != nil {
		t.Fatalf("CreateAuthService: %v", err)
	}
	if err := store.RevokeAuthService(id); err != nil {
		t.Fatalf("RevokeAuthService: %v", err)
	}

	verifier, err := NewAccountServiceVerifier("", store)
	if err != nil {
		t.Fatalf("NewAccountServiceVerifier: %v", err)
	}
	if _, ok := verifier.Verify(token); ok {
		t.Fatal("unexpected verification success for revoked token")
	}
}

func TestAccountCenterIntrospectActiveToken(t *testing.T) {
	oldSecret := append([]byte(nil), jwtSecret...)
	defer func() { jwtSecret = oldSecret }()
	SetJWTSecret("account-center-test-secret")
	token, err := GenerateToken(42, "alice", "alice@example.com")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	verifier, err := NewEnvAccountServiceVerifier("relay=service-secret")
	if err != nil {
		t.Fatalf("NewEnvAccountServiceVerifier: %v", err)
	}
	handler := NewAccountCenterHandler(accountTestUserLookup{users: map[int64]*types.User{
		42: {
			ID:          42,
			Username:    "alice",
			Email:       "alice@example.com",
			DisplayName: "Alice",
			AccountType: types.AccountHuman,
			State:       0,
			CreatedAt:   time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
		},
	}}, verifier, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/account/introspect", strings.NewReader(`{"token":"`+token+`"}`))
	req.Header.Set("Authorization", "Service service-secret")
	rec := httptest.NewRecorder()

	handler.HandleIntrospect(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["active"] != true {
		t.Fatalf("expected active token, got %v", body)
	}
	user, ok := body["user"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing user payload: %v", body)
	}
	if user["uid"].(float64) != 42 || user["username"] != "alice" {
		t.Fatalf("unexpected user payload: %v", user)
	}
}

func TestAccountCenterIntrospectInvalidUserToken(t *testing.T) {
	verifier, err := NewEnvAccountServiceVerifier("relay=service-secret")
	if err != nil {
		t.Fatalf("NewEnvAccountServiceVerifier: %v", err)
	}
	handler := NewAccountCenterHandler(accountTestUserLookup{users: map[int64]*types.User{}}, verifier, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/account/introspect", strings.NewReader(`{"token":"not-a-jwt"}`))
	req.Header.Set("Authorization", "Service service-secret")
	rec := httptest.NewRecorder()

	handler.HandleIntrospect(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["active"] != false || body["error"] != "invalid_or_expired_token" {
		t.Fatalf("unexpected invalid token response: %v", body)
	}
}

func TestAccountCenterRejectsMissingServiceToken(t *testing.T) {
	verifier, err := NewEnvAccountServiceVerifier("relay=service-secret")
	if err != nil {
		t.Fatalf("NewEnvAccountServiceVerifier: %v", err)
	}
	handler := NewAccountCenterHandler(accountTestUserLookup{users: map[int64]*types.User{}}, verifier, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/account/introspect", strings.NewReader(`{"token":"x"}`))
	rec := httptest.NewRecorder()

	handler.HandleIntrospect(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAccountCenterGetUser(t *testing.T) {
	verifier, err := NewEnvAccountServiceVerifier("relay=service-secret")
	if err != nil {
		t.Fatalf("NewEnvAccountServiceVerifier: %v", err)
	}
	handler := NewAccountCenterHandler(accountTestUserLookup{users: map[int64]*types.User{
		7: {
			ID:          7,
			Username:    "bob",
			DisplayName: "Bob",
			AccountType: types.AccountHuman,
			State:       0,
		},
	}}, verifier, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/account/users/7", nil)
	req.Header.Set("Authorization", "Service service-secret")
	rec := httptest.NewRecorder()

	handler.HandleGetUser(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["uid"].(float64) != 7 || body["username"] != "bob" {
		t.Fatalf("unexpected user response: %v", body)
	}
}

func TestAccountCenterCreatesAndListsUserAPIKey(t *testing.T) {
	store := newAccountTestAuthServiceStore()
	handler := NewAccountCenterHandler(accountTestUserLookup{users: map[int64]*types.User{}}, nil, store)

	req := httptest.NewRequest(http.MethodPost, "/api/account/api-keys", strings.NewReader(`{"service":"cats-relay","name":"Relay Key","scopes":["relay.chat"]}`))
	req = withTestUID(req, 42)
	rec := httptest.NewRecorder()
	handler.HandleAPIKeys(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Key  string `json:"key"`
		Meta struct {
			ID      int64  `json:"id"`
			Service string `json:"service"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.HasPrefix(created.Key, "cat_sk_") || created.Meta.Service != "cats-relay" {
		t.Fatalf("unexpected create response: %+v", created)
	}
	if store.keys[created.Meta.ID].KeyHash == "" || store.keys[created.Meta.ID].KeyHash == created.Key {
		t.Fatalf("api key was not hashed in store: %+v", store.keys[created.Meta.ID])
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/account/api-keys", nil)
	listReq = withTestUID(listReq, 42)
	listRec := httptest.NewRecorder()
	handler.HandleAPIKeys(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	var listed struct {
		Keys []types.AuthAPIKey `json:"keys"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listed.Keys) != 1 || listed.Keys[0].KeyHash != "" {
		t.Fatalf("unexpected list response: %+v", listed)
	}
}

func TestAccountCenterIntrospectsUserAPIKey(t *testing.T) {
	store := newAccountTestAuthServiceStore()
	_, err := store.CreateAuthService(&types.AuthService{
		Slug:        "cats-relay",
		Name:        "Cats Relay",
		TokenPrefix: "cats_svc_test",
		TokenHash:   HashAccountServiceToken("service-secret"),
		Scopes:      []string{"account.introspect"},
		State:       0,
	})
	if err != nil {
		t.Fatalf("CreateAuthService: %v", err)
	}
	apiKey, prefix, hash, err := GenerateAccountAPIKey()
	if err != nil {
		t.Fatalf("GenerateAccountAPIKey: %v", err)
	}
	if _, err := store.CreateAuthAPIKey(&types.AuthAPIKey{
		OwnerUserID: 42,
		ServiceSlug: "cats-relay",
		Name:        "Relay Key",
		KeyPrefix:   prefix,
		KeyHash:     hash,
		Scopes:      []string{"relay.chat"},
		State:       0,
	}); err != nil {
		t.Fatalf("CreateAuthAPIKey: %v", err)
	}
	verifier, err := NewAccountServiceVerifier("", store)
	if err != nil {
		t.Fatalf("NewAccountServiceVerifier: %v", err)
	}
	handler := NewAccountCenterHandler(accountTestUserLookup{users: map[int64]*types.User{
		42: {
			ID:          42,
			Username:    "alice",
			Email:       "alice@example.com",
			DisplayName: "Alice",
			AccountType: types.AccountHuman,
			State:       0,
		},
	}}, verifier, store)

	req := httptest.NewRequest(http.MethodPost, "/api/account/api-keys/introspect", strings.NewReader(`{"key":"`+apiKey+`","required_scope":"relay.chat"}`))
	req.Header.Set("Authorization", "Service service-secret")
	rec := httptest.NewRecorder()
	handler.HandleAPIKeyIntrospect(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["active"] != true || body["uid"].(float64) != 42 {
		t.Fatalf("unexpected introspection response: %v", body)
	}
}

func TestAccountCenterIntrospectRejectsWrongScope(t *testing.T) {
	store := newAccountTestAuthServiceStore()
	_, err := store.CreateAuthService(&types.AuthService{
		Slug:      "cats-relay",
		Name:      "Cats Relay",
		TokenHash: HashAccountServiceToken("service-secret"),
		State:     0,
	})
	if err != nil {
		t.Fatalf("CreateAuthService: %v", err)
	}
	apiKey, prefix, hash, err := GenerateAccountAPIKey()
	if err != nil {
		t.Fatalf("GenerateAccountAPIKey: %v", err)
	}
	if _, err := store.CreateAuthAPIKey(&types.AuthAPIKey{
		OwnerUserID: 42,
		ServiceSlug: "cats-relay",
		Name:        "Relay Key",
		KeyPrefix:   prefix,
		KeyHash:     hash,
		Scopes:      []string{"relay.models.read"},
		State:       0,
	}); err != nil {
		t.Fatalf("CreateAuthAPIKey: %v", err)
	}
	verifier, err := NewAccountServiceVerifier("", store)
	if err != nil {
		t.Fatalf("NewAccountServiceVerifier: %v", err)
	}
	handler := NewAccountCenterHandler(accountTestUserLookup{users: map[int64]*types.User{}}, verifier, store)

	req := httptest.NewRequest(http.MethodPost, "/api/account/api-keys/introspect", strings.NewReader(`{"key":"`+apiKey+`","required_scope":"relay.chat"}`))
	req.Header.Set("Authorization", "Service service-secret")
	rec := httptest.NewRecorder()
	handler.HandleAPIKeyIntrospect(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["active"] != false || body["error"] != "scope_denied" {
		t.Fatalf("unexpected scope response: %v", body)
	}
}
