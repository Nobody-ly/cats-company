package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openchat/openchat/server/store/types"
)

func TestAccountAdminPageAllowsTunnelAddress(t *testing.T) {
	handler := NewAccountAdminHandler(accountTestUserLookup{users: map[int64]*types.User{}}, nil, nil)

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
	handler := NewAccountAdminHandler(accountTestUserLookup{users: map[int64]*types.User{}}, nil, nil)

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
	}}, verifier, nil)

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

type accountTestAuthServiceStore struct {
	nextID    int64
	nextKeyID int64
	services  map[int64]*types.AuthService
	keys      map[int64]*types.AuthAPIKey
}

func newAccountTestAuthServiceStore() *accountTestAuthServiceStore {
	return &accountTestAuthServiceStore{
		nextID:    1,
		nextKeyID: 1,
		services:  map[int64]*types.AuthService{},
		keys:      map[int64]*types.AuthAPIKey{},
	}
}

func (s *accountTestAuthServiceStore) CreateAuthService(service *types.AuthService) (int64, error) {
	if s.nextID == 0 {
		s.nextID = 1
	}
	for id, existing := range s.services {
		if existing.Slug == service.Slug {
			cp := *service
			cp.ID = id
			s.services[id] = &cp
			return id, nil
		}
	}
	id := s.nextID
	s.nextID++
	cp := *service
	cp.ID = id
	s.services[id] = &cp
	return id, nil
}

func (s *accountTestAuthServiceStore) ListAuthServices() ([]*types.AuthService, error) {
	out := make([]*types.AuthService, 0, len(s.services))
	for _, service := range s.services {
		out = append(out, service)
	}
	return out, nil
}

func (s *accountTestAuthServiceStore) GetAuthServiceByTokenHash(tokenHash string) (*types.AuthService, error) {
	for _, service := range s.services {
		if service.TokenHash == tokenHash && service.State == 0 {
			return service, nil
		}
	}
	return nil, nil
}

func (s *accountTestAuthServiceStore) RevokeAuthService(id int64) error {
	if service := s.services[id]; service != nil {
		service.State = 1
	}
	return nil
}

func (s *accountTestAuthServiceStore) TouchAuthServiceLastUsed(id int64) error {
	return nil
}

func (s *accountTestAuthServiceStore) CreateAuthAPIKey(key *types.AuthAPIKey) (int64, error) {
	if s.nextKeyID == 0 {
		s.nextKeyID = 1
	}
	id := s.nextKeyID
	s.nextKeyID++
	cp := *key
	cp.ID = id
	s.keys[id] = &cp
	return id, nil
}

func (s *accountTestAuthServiceStore) ListAuthAPIKeysByOwner(ownerUserID int64) ([]*types.AuthAPIKey, error) {
	out := []*types.AuthAPIKey{}
	for _, key := range s.keys {
		if key.OwnerUserID == ownerUserID {
			out = append(out, key)
		}
	}
	return out, nil
}

func (s *accountTestAuthServiceStore) GetAuthAPIKeyByHash(keyHash string) (*types.AuthAPIKey, error) {
	for _, key := range s.keys {
		if key.KeyHash == keyHash && key.State == 0 {
			return key, nil
		}
	}
	return nil, nil
}

func (s *accountTestAuthServiceStore) RevokeAuthAPIKey(ownerUserID, id int64) error {
	if key := s.keys[id]; key != nil && key.OwnerUserID == ownerUserID {
		key.State = 1
	}
	return nil
}

func (s *accountTestAuthServiceStore) TouchAuthAPIKeyLastUsed(id int64) error {
	return nil
}

func TestAccountAdminCreatesAndRevokesService(t *testing.T) {
	store := newAccountTestAuthServiceStore()
	handler := NewAccountAdminHandler(accountTestUserLookup{users: map[int64]*types.User{}}, nil, store)

	req := httptest.NewRequest(http.MethodPost, "/local/account-admin/services", strings.NewReader(`{"slug":"cats-relay","name":"Cats Relay","scopes":["account.introspect"]}`))
	req.RemoteAddr = "127.0.0.1:40200"
	rec := httptest.NewRecorder()
	handler.HandleServices(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Token   string `json:"token"`
		Service struct {
			ID   int64  `json:"id"`
			Slug string `json:"slug"`
		} `json:"service"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if created.Token == "" || created.Service.Slug != "cats-relay" {
		t.Fatalf("unexpected create response: %+v", created)
	}

	revokeReq := httptest.NewRequest(http.MethodPost, "/local/account-admin/services/revoke", strings.NewReader(`{"id":1}`))
	revokeReq.RemoteAddr = "127.0.0.1:40200"
	revokeRec := httptest.NewRecorder()
	handler.HandleRevokeService(revokeRec, revokeReq)

	if revokeRec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", revokeRec.Code, revokeRec.Body.String())
	}
	if store.services[1].State != 1 {
		t.Fatalf("expected revoked service, got state=%d", store.services[1].State)
	}
}
