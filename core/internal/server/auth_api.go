package server

import "net/http"

// handleAuthStatus exposes whether an owner has been claimed and whether
// JWT verification is enabled. Called pre-login by Studio to decide
// between "sign up to claim" and "log in as the owner". Public route.
func (s *Server) handleAuthStatus(w http.ResponseWriter, _ *http.Request) {
	resp := map[string]any{
		"enabled":      false,
		"owner_set":    false,
		"accept_signup": true,
	}
	if s.auth != nil {
		resp["enabled"] = s.auth.Enabled()
		owner := s.auth.Owner()
		resp["owner_set"] = owner != ""
		// Once an owner is set, signup of new users would still succeed at
		// Supabase but the Core gate rejects them. Studio uses this flag to
		// hide the "sign up" tab and surface a clear message.
		resp["accept_signup"] = owner == ""
	}
	writeJSON(w, http.StatusOK, resp)
}
