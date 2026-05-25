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

func TestAccountAdminUserSearchByEmail(t *testing.T) {
	handler := NewAccountAdminHandler(accountTestUserLookup{users: map[int64]*types.User{
		12: {
			ID:          12,
			Username:    "dora",
			Email:       "dora@example.com",
			DisplayName: "Dora",
			AccountType: types.AccountHuman,
			State:       0,
		},
	}}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/local/account-admin/users/search?q=dora@example.com", nil)
	req.RemoteAddr = "127.0.0.1:40200"
	rec := httptest.NewRecorder()
	handler.HandleUserSearch(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Users []struct {
			UID   int64  `json:"uid"`
			Email string `json:"email"`
		} `json:"users"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Count != 1 || len(body.Users) != 1 || body.Users[0].UID != 12 || body.Users[0].Email != "dora@example.com" {
		t.Fatalf("unexpected search response: %+v", body)
	}
}

func TestAccountAdminUserSearchDeduplicatesUsernameAndFuzzyMatches(t *testing.T) {
	handler := NewAccountAdminHandler(accountTestUserLookup{users: map[int64]*types.User{
		21: {
			ID:          21,
			Username:    "carol",
			Email:       "carol@example.com",
			DisplayName: "Carol",
			AccountType: types.AccountHuman,
			State:       0,
		},
		22: {
			ID:          22,
			Username:    "carol-helper",
			Email:       "helper@example.com",
			DisplayName: "Carol Helper",
			AccountType: types.AccountHuman,
			State:       0,
		},
	}}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/local/account-admin/users/search?q=carol", nil)
	req.RemoteAddr = "127.0.0.1:40200"
	rec := httptest.NewRecorder()
	handler.HandleUserSearch(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Users []struct {
			UID int64 `json:"uid"`
		} `json:"users"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Count != 2 || len(body.Users) != 2 {
		t.Fatalf("expected two deduplicated users, got %+v", body)
	}
	seen := map[int64]bool{}
	for _, user := range body.Users {
		if seen[user.UID] {
			t.Fatalf("duplicate uid in search response: %+v", body)
		}
		seen[user.UID] = true
	}
	if !seen[21] || !seen[22] {
		t.Fatalf("missing expected users: %+v", body)
	}
}

type accountTestAuthServiceStore struct {
	nextID   int64
	services map[int64]*types.AuthService
}

func newAccountTestAuthServiceStore() *accountTestAuthServiceStore {
	return &accountTestAuthServiceStore{
		nextID:   1,
		services: map[int64]*types.AuthService{},
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

type accountNilAuthServiceStore struct{}

func (accountNilAuthServiceStore) CreateAuthService(service *types.AuthService) (int64, error) {
	return 0, nil
}

func (accountNilAuthServiceStore) ListAuthServices() ([]*types.AuthService, error) {
	return nil, nil
}

func (accountNilAuthServiceStore) RevokeAuthService(id int64) error {
	return nil
}

func TestAccountAdminListsEmptyServicesAsArray(t *testing.T) {
	handler := NewAccountAdminHandler(accountTestUserLookup{users: map[int64]*types.User{}}, nil, accountNilAuthServiceStore{})

	req := httptest.NewRequest(http.MethodGet, "/local/account-admin/services", nil)
	req.RemoteAddr = "127.0.0.1:40200"
	rec := httptest.NewRecorder()
	handler.HandleServices(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Services []types.AuthService `json:"services"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Services == nil {
		t.Fatalf("expected empty services array, got nil: %s", rec.Body.String())
	}
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
