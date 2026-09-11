package api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/provider/ankrmgmt"
)

func (s *Server) handleAnkrManagementConnect(w http.ResponseWriter, r *http.Request) {
	if s.ankrMgmt == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "Ankr Management MCP is unavailable"})
		return
	}
	startOAuth := s.ankrMgmt.StartOAuth
	if r.URL.Query().Get("reset_connection") == "true" {
		startOAuth = s.ankrMgmt.RestartOAuth
	}
	authorizationURL, err := startOAuth(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authorization_url": authorizationURL})
}

func (s *Server) handleAnkrManagementCallback(w http.ResponseWriter, r *http.Request) {
	redirect := "/api-control?tab=providers"
	if s.ankrMgmt != nil && strings.TrimSpace(s.ankrMgmt.FrontendURL()) != "" {
		redirect = s.ankrMgmt.FrontendURL()
	}
	resultURL, err := url.Parse(redirect)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "invalid frontend redirect"})
		return
	}
	query := resultURL.Query()
	if oauthError := r.URL.Query().Get("error"); oauthError != "" {
		query.Set("ankr_oauth", "error")
		query.Set("ankr_message", r.URL.Query().Get("error_description"))
		resultURL.RawQuery = query.Encode()
		http.Redirect(w, r, resultURL.String(), http.StatusFound)
		return
	}
	if s.ankrMgmt == nil {
		query.Set("ankr_oauth", "error")
		query.Set("ankr_message", "Ankr Management MCP is unavailable")
	} else if err := s.ankrMgmt.CompleteOAuth(r.Context(), r.URL.Query().Get("state"), r.URL.Query().Get("code")); err != nil {
		query.Set("ankr_oauth", "error")
		query.Set("ankr_message", err.Error())
	} else {
		query.Set("ankr_oauth", "connected")
	}
	resultURL.RawQuery = query.Encode()
	http.Redirect(w, r, resultURL.String(), http.StatusFound)
}

func (s *Server) handleAnkrManagementSync(w http.ResponseWriter, r *http.Request) {
	if s.ankrMgmt == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "Ankr Management MCP is unavailable"})
		return
	}
	account, err := s.ankrMgmt.Sync(r.Context())
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, ankrmgmt.ErrAuthorizationRequired) {
			status = http.StatusUnauthorized
		}
		writeJSON(w, status, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": account})
}

func (s *Server) handleAnkrManagementDisconnect(w http.ResponseWriter, r *http.Request) {
	if s.ankrMgmt == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "Ankr Management MCP is unavailable"})
		return
	}
	if err := s.ankrMgmt.Disconnect(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "disconnected"})
}
