package server

import (
	"embed"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
)

//go:embed account_admin.html
var accountAdminAssets embed.FS

// AccountAdminHandler exposes a tiny local-only operator UI for the account
// center. It intentionally lives outside /api so public nginx routes do not
// expose it.
type AccountAdminHandler struct {
	users    AccountUserLookup
	services AccountServiceVerifier
}

func NewAccountAdminHandler(users AccountUserLookup, services AccountServiceVerifier) *AccountAdminHandler {
	return &AccountAdminHandler{users: users, services: services}
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

func writeAccountAdminJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
