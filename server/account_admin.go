package server

import (
	"embed"
	"encoding/json"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/openchat/openchat/server/store/types"
)

//go:embed account_admin.html
var accountAdminAssets embed.FS

// AccountAdminHandler exposes a tiny local-only operator UI for the account
// center. It intentionally lives outside /api so public nginx routes do not
// expose it.
type AccountAdminHandler struct {
	users        AccountUserLookup
	services     AccountServiceVerifier
	serviceStore AccountAdminServiceStore
}

type AccountAdminServiceStore interface {
	CreateAuthService(service *types.AuthService) (int64, error)
	ListAuthServices() ([]*types.AuthService, error)
	RevokeAuthService(id int64) error
}

func NewAccountAdminHandler(users AccountUserLookup, services AccountServiceVerifier, serviceStore AccountAdminServiceStore) *AccountAdminHandler {
	return &AccountAdminHandler{users: users, services: services, serviceStore: serviceStore}
}

func (h *AccountAdminHandler) HandlePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAccountAdminJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.requireLocal(w, r) {
		return
	}

	body, err := accountAdminAssets.ReadFile("account_admin.html")
	if err != nil {
		writeAccountAdminJSON(w, http.StatusInternalServerError, map[string]string{"error": "admin page unavailable"})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

func (h *AccountAdminHandler) HandleUserLookup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAccountAdminJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.requireLocal(w, r) {
		return
	}

	uid, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("uid")), 10, 64)
	if err != nil || uid <= 0 {
		writeAccountAdminJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid uid"})
		return
	}

	user, err := h.users.GetUser(uid)
	if err != nil {
		writeAccountAdminJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}
	if user == nil {
		writeAccountAdminJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}

	writeAccountAdminJSON(w, http.StatusOK, map[string]interface{}{
		"user": accountUserPayload(user),
		"account_center": map[string]interface{}{
			"service_tokens_configured": h.services != nil && h.services.Configured(),
		},
	})
}

func (h *AccountAdminHandler) HandleServices(w http.ResponseWriter, r *http.Request) {
	if !h.requireLocal(w, r) {
		return
	}
	if h.serviceStore == nil {
		writeAccountAdminJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "auth service store unavailable"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.handleListServices(w, r)
	case http.MethodPost:
		h.handleCreateService(w, r)
	default:
		writeAccountAdminJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (h *AccountAdminHandler) HandleRevokeService(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAccountAdminJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.requireLocal(w, r) {
		return
	}
	if h.serviceStore == nil {
		writeAccountAdminJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "auth service store unavailable"})
		return
	}

	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeAccountAdminJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid service id"})
		return
	}
	if err := h.serviceStore.RevokeAuthService(req.ID); err != nil {
		writeAccountAdminJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to revoke service"})
		return
	}
	writeAccountAdminJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (h *AccountAdminHandler) handleListServices(w http.ResponseWriter, _ *http.Request) {
	services, err := h.serviceStore.ListAuthServices()
	if err != nil {
		writeAccountAdminJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list services"})
		return
	}
	writeAccountAdminJSON(w, http.StatusOK, map[string]interface{}{"services": services})
}

func (h *AccountAdminHandler) handleCreateService(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Slug   string   `json:"slug"`
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAccountAdminJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	slug := normalizeAuthServiceSlug(req.Slug)
	if slug == "" {
		writeAccountAdminJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid slug"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = slug
	}
	scopes := normalizeAuthServiceScopes(req.Scopes)
	token, tokenPrefix, tokenHash, err := GenerateAccountServiceToken()
	if err != nil {
		writeAccountAdminJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate token"})
		return
	}

	id, err := h.serviceStore.CreateAuthService(&types.AuthService{
		Slug:        slug,
		Name:        name,
		TokenPrefix: tokenPrefix,
		TokenHash:   tokenHash,
		Scopes:      scopes,
		State:       0,
	})
	if err != nil {
		writeAccountAdminJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create service"})
		return
	}

	writeAccountAdminJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true,
		"service": map[string]interface{}{
			"id":           id,
			"slug":         slug,
			"name":         name,
			"token_prefix": tokenPrefix,
			"scopes":       scopes,
			"state":        0,
		},
		"token": token,
	})
}

func (h *AccountAdminHandler) requireLocal(w http.ResponseWriter, r *http.Request) bool {
	if isLocalAdminRequest(r) {
		return true
	}
	writeAccountAdminJSON(w, http.StatusForbidden, map[string]string{"error": "local access only"})
	return false
}

func isLocalAdminRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return false
	}
	// SSH tunnels and Docker host port forwarding commonly appear as loopback
	// or a private bridge address inside the container. Public clients are not
	// accepted here, and the route is not exposed by public nginx config.
	return ip.IsLoopback() || ip.IsPrivate()
}

var authServiceSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,62}[a-z0-9]$`)

func normalizeAuthServiceSlug(slug string) string {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if !authServiceSlugPattern.MatchString(slug) {
		return ""
	}
	return slug
}

func normalizeAuthServiceScopes(scopes []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, scope := range scopes {
		scope = strings.ToLower(strings.TrimSpace(scope))
		if scope == "" || seen[scope] {
			continue
		}
		seen[scope] = true
		out = append(out, scope)
	}
	return out
}

func writeAccountAdminJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
