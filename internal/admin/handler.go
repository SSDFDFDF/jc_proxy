package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"jc_proxy/internal/config"
	"jc_proxy/internal/keystore"
)

type Handler struct {
	service    *Service
	sessions   *SessionManager
	loginGuard *LoginGuard
}

func NewHandler(service *Service, sessions *SessionManager) *Handler {
	return &Handler{
		service:    service,
		sessions:   sessions,
		loginGuard: NewLoginGuard(),
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/admin/login", h.handleLogin)
	mux.HandleFunc("/admin/logout", h.withAuth(h.handleLogout))
	mux.HandleFunc("/admin/me", h.withAuth(h.handleMe))

	mux.HandleFunc("/admin/config", h.withAuth(h.handleConfig))
	mux.HandleFunc("/admin/config/raw", h.withAuth(h.handleConfigRaw))
	mux.HandleFunc("/admin/stats", h.withAuth(h.handleStats))
	mux.HandleFunc("/admin/upstream-keys", h.withAuth(h.handleUpstreamKeyIndex))
	mux.HandleFunc("/admin/upstream-keys/", h.withAuth(h.handleUpstreamKeyVendor))

	mux.HandleFunc("/admin/password", h.withAuth(h.handlePasswordRotate))
	mux.HandleFunc("/admin/vendors", h.withAuth(h.handleVendors))
	mux.HandleFunc("/admin/vendors/", h.withAuth(h.handleVendorByPath))
}

func (h *Handler) allowRequest(w http.ResponseWriter, r *http.Request) (netip.Addr, bool) {
	enabled, allowed, trusted, err := h.service.AdminAccess()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return netip.Addr{}, false
	}
	if !enabled {
		http.NotFound(w, r)
		return netip.Addr{}, false
	}
	addr, err := config.ResolveRequestAddr(r.RemoteAddr, r.Header, trusted)
	if err != nil || !config.AddrAllowed(addr, allowed) {
		http.NotFound(w, r)
		return netip.Addr{}, false
	}
	return addr, true
}

func (h *Handler) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := h.allowRequest(w, r); !ok {
			return
		}
		token := extractToken(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, "missing admin token")
			return
		}
		s, ok := h.sessions.Validate(token)
		if !ok {
			writeError(w, http.StatusUnauthorized, "invalid or expired admin token")
			return
		}
		r.Header.Set("X-Admin-User", s.Username)
		next(w, r)
	}
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	clientAddr, ok := h.allowRequest(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	clientKey := clientAddr.String()
	if wait, blocked := h.loginGuard.Blocked(clientKey); blocked {
		h.auditLoginEvent(strings.TrimSpace(req.Username), "admin.login.rate_limited", clientAddr, map[string]any{
			"retry_after_seconds": int(wait.Round(time.Second) / time.Second),
		})
		writeError(w, http.StatusTooManyRequests, rateLimitMessage(wait))
		return
	}
	token, exp, err := h.service.Login(req.Username, req.Password)
	if err != nil {
		wait := h.loginGuard.Failed(clientKey)
		action := "admin.login.failed"
		status := http.StatusUnauthorized
		message := err.Error()
		detail := map[string]any{}
		if wait > 0 {
			action = "admin.login.rate_limited"
			status = http.StatusTooManyRequests
			message = rateLimitMessage(wait)
			detail["retry_after_seconds"] = int(wait.Round(time.Second) / time.Second)
		}
		h.auditLoginEvent(strings.TrimSpace(req.Username), action, clientAddr, detail)
		writeError(w, status, message)
		return
	}
	h.loginGuard.Succeeded(clientKey)
	writeJSON(w, http.StatusOK, LoginResponse{Token: token, ExpiresAt: exp, Username: req.Username})
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	token := extractToken(r)
	h.service.Logout(token, r.Header.Get("X-Admin-User"))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"username": r.Header.Get("X-Admin-User")})
}

func (h *Handler) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := h.service.GetConfigMasked()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, cfg)
	case http.MethodPut:
		var next config.Config
		if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
			writeError(w, http.StatusBadRequest, "invalid config json")
			return
		}
		if err := h.service.UpdateConfig(r.Header.Get("X-Admin-User"), &next); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) handleConfigRaw(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	cfg, err := h.service.GetConfigRaw()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (h *Handler) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	page, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("page")))
	pageSize, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("page_size")))
	writeJSON(w, http.StatusOK, h.service.Stats(RuntimeStatsQuery{
		Vendor:   strings.TrimSpace(r.URL.Query().Get("vendor")),
		Filter:   strings.TrimSpace(r.URL.Query().Get("filter")),
		Q:        strings.TrimSpace(r.URL.Query().Get("q")),
		Page:     page,
		PageSize: pageSize,
	}))
}

func (h *Handler) handleUpstreamKeyIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	resp, err := h.service.ListUpstreamKeys()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleUpstreamKeyVendor(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.Trim(strings.TrimPrefix(r.URL.Path, "/admin/upstream-keys/"), "/")
	if trimmed == "" {
		writeError(w, http.StatusBadRequest, "vendor path required")
		return
	}
	parts := strings.Split(trimmed, "/")
	vendor := strings.TrimSpace(parts[0])
	action := ""
	if len(parts) > 1 {
		action = strings.TrimSpace(parts[1])
	}
	if vendor == "" {
		writeError(w, http.StatusBadRequest, "vendor path required")
		return
	}

	if action != "" {
		h.handleUpstreamKeyStatusAction(w, r, vendor, action)
		return
	}

	switch r.Method {
	case http.MethodPut:
		var req UpstreamKeysReplaceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		if err := h.service.ReplaceUpstreamKeys(r.Header.Get("X-Admin-User"), vendor, req.Keys); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case http.MethodPost:
		keys, err := decodeKeysPayload(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		for _, key := range keys {
			if err := h.service.AddUpstreamKey(r.Header.Get("X-Admin-User"), vendor, key); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case http.MethodDelete:
		keys, err := decodeKeysPayload(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		for _, key := range keys {
			if err := h.service.DeleteUpstreamKey(r.Header.Get("X-Admin-User"), vendor, key); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) handleUpstreamKeyStatusAction(w http.ResponseWriter, r *http.Request, vendor, action string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req UpstreamKeyStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	actor := r.Header.Get("X-Admin-User")

	switch action {
	case "enable":
		if err := h.service.EnableUpstreamKey(actor, vendor, req.Key); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	case "disable":
		if err := h.service.DisableUpstreamKey(actor, vendor, req.Key, req.Reason, keystore.KeyStatusDisabledManual); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	default:
		writeError(w, http.StatusNotFound, "unknown upstream key action")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handlePasswordRotate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if err := h.service.RotatePassword(r.Header.Get("X-Admin-User"), req.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handleVendors(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := h.service.GetConfigMasked()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, cfg.Vendors)
	case http.MethodPost:
		var req VendorUpsertRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		if err := h.service.UpsertVendor(r.Header.Get("X-Admin-User"), req.Vendor, req.Config); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) handleVendorByPath(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/admin/vendors/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		writeError(w, http.StatusBadRequest, "vendor path required")
		return
	}
	vendor := parts[0]

	if len(parts) == 1 {
		if r.Method != http.MethodDelete {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if err := h.service.DeleteVendor(r.Header.Get("X-Admin-User"), vendor); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	resource := parts[1]
	if resource == "keys" {
		h.handleUpstreamKeys(w, r, vendor)
		return
	}
	if resource == "client-keys" {
		h.handleClientKeys(w, r, vendor)
		return
	}
	writeError(w, http.StatusNotFound, "unknown vendor resource")
}

func (h *Handler) handleUpstreamKeys(w http.ResponseWriter, r *http.Request, vendor string) {
	var req KeyCreateRequest
	switch r.Method {
	case http.MethodPost:
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		if err := h.service.AddUpstreamKey(r.Header.Get("X-Admin-User"), vendor, req.Key); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case http.MethodDelete:
		var del KeyDeleteRequest
		if err := json.NewDecoder(r.Body).Decode(&del); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		if err := h.service.DeleteUpstreamKey(r.Header.Get("X-Admin-User"), vendor, del.Key); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) handleClientKeys(w http.ResponseWriter, r *http.Request, vendor string) {
	switch r.Method {
	case http.MethodPost:
		var req KeyCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		if err := h.service.AddClientKey(r.Header.Get("X-Admin-User"), vendor, req.Key); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case http.MethodDelete:
		var req KeyDeleteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		if err := h.service.DeleteClientKey(r.Header.Get("X-Admin-User"), vendor, req.Key); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func extractToken(r *http.Request) string {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	if v := strings.TrimSpace(r.Header.Get("X-Admin-Token")); v != "" {
		return v
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	if msg == "" {
		msg = http.StatusText(status)
	}
	writeJSON(w, status, map[string]any{"error": msg})
}

func decodeKeysPayload(r *http.Request) ([]string, error) {
	var batch struct {
		Key  string   `json:"key"`
		Keys []string `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
		return nil, errors.New("invalid json body")
	}
	if len(batch.Keys) > 0 {
		return batch.Keys, nil
	}
	if strings.TrimSpace(batch.Key) != "" {
		return []string{batch.Key}, nil
	}
	return nil, errors.New("key or keys is required")
}

func (h *Handler) auditLoginEvent(actor, action string, clientAddr netip.Addr, detail map[string]any) {
	if h == nil || h.service == nil || h.service.audit == nil {
		return
	}
	if detail == nil {
		detail = map[string]any{}
	}
	if clientAddr.IsValid() {
		detail["client_ip"] = clientAddr.String()
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		actor = "anonymous"
	}
	h.service.audit.Log(actor, action, detail)
}

func rateLimitMessage(wait time.Duration) string {
	seconds := int(wait.Round(time.Second) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf("too many login attempts, retry in %ds", seconds)
}
