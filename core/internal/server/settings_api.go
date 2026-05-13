package server

import (
	"context"
	"encoding/json"
	"net/http"
)

// settingsModelResponse is the on-the-wire shape for GET/PUT
// /api/settings/model. `model` is the effective id (override if set,
// boot default otherwise). `provider` is the loop's current provider
// name — useful so the UI can filter the model picker to options that
// belong to the wired provider. `default_model` is the boot default,
// surfaced so the UI can show "(default)" next to the right entry. The
// `source` field tells Studio whether the user has explicitly chosen
// ("user") or is riding the boot default ("default") — drives chip UX.
type settingsModelResponse struct {
	Model        string   `json:"model"`
	DefaultModel string   `json:"default_model"`
	Provider     string   `json:"provider"`
	Source       string   `json:"source"`
	// AvailableProviders is the set of provider ids the runtime knows
	// how to swap to (creds present + registry-registered). Studio uses
	// it to gray out vendor picker rows whose credentials aren't wired.
	AvailableProviders []string `json:"available_providers"`
	// ProviderSource: "user" when the active provider came from the
	// settings store (Studio picker), "default" when it's the env-set
	// LLM_PROVIDER. Mirrors the existing Source field for the model.
	ProviderSource string `json:"provider_source"`
}

// handleSettingsModel serves GET + PUT /api/settings/model.
//
// GET → returns the effective model id + provider + source.
// PUT body: {"model": "claude-opus-4-7"}. Empty model clears the override
// so the loop falls back to the boot default. We don't allowlist values
// server-side beyond a trim — the Anthropic / OpenAI / Google APIs are
// the source of truth for what's valid, and they return clean errors
// that the WS error channel already plumbs back to Studio. A typo here
// produces a visible "invalid model id" on the next turn rather than a
// silent save failure that confuses the user.
func (s *Server) handleSettingsModel(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.buildSettingsModelResponse(r.Context()))
	case http.MethodPut, http.MethodPost:
		if s.settings == nil {
			writeJSON(w, http.StatusServiceUnavailable,
				map[string]string{"error": "settings store not configured"})
			return
		}
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest,
				map[string]string{"error": "invalid JSON"})
			return
		}
		if err := s.settings.SetModel(r.Context(), body.Model); err != nil {
			writeJSON(w, http.StatusInternalServerError,
				map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, s.buildSettingsModelResponse(r.Context()))
	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// buildSettingsModelResponse assembles the canonical wire shape for the
// model setting. Used by GET and the PUT echo so they share the same
// payload format and the UI can update from either response.
func (s *Server) buildSettingsModelResponse(ctx context.Context) settingsModelResponse {
	resp := settingsModelResponse{Source: "default", ProviderSource: "default"}
	if s.loop != nil {
		if p := s.loop.Provider(); p != nil {
			resp.Provider = p.Name()
			resp.DefaultModel = p.Model()
			resp.Model = p.Model()
		}
	}
	if s.settings != nil {
		if override := s.settings.GetModel(ctx); override != "" {
			resp.Model = override
			resp.Source = "user"
		}
		if persisted := s.settings.GetProvider(ctx); persisted != "" {
			resp.ProviderSource = "user"
		}
	}
	if s.llmReg != nil {
		resp.AvailableProviders = s.llmReg.Available()
	}
	return resp
}

// handleSettingsProvider serves GET + PUT /api/settings/provider. The PUT
// hot-swaps the agent loop's provider via Loop.SetProvider and persists
// the choice in the settings store. Switching providers does NOT touch
// mem_provider_tokens — flipping anthropic → openai_oauth → anthropic
// never requires re-authentication.
//
// PUT body: {"provider": "openai_oauth"}. Empty value clears the override
// and reverts to the LLM_PROVIDER env (next restart only — runtime stays
// on whatever's active until then).
func (s *Server) handleSettingsProvider(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.buildSettingsModelResponse(r.Context()))
	case http.MethodPut, http.MethodPost:
		if s.settings == nil {
			writeJSON(w, http.StatusServiceUnavailable,
				map[string]string{"error": "settings store not configured"})
			return
		}
		if s.llmReg == nil || s.loop == nil {
			writeJSON(w, http.StatusServiceUnavailable,
				map[string]string{"error": "provider registry not configured"})
			return
		}
		var body struct {
			Provider string `json:"provider"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		target := body.Provider
		if target != "" {
			p, ok := s.llmReg.Get(target)
			if !ok {
				writeJSON(w, http.StatusBadRequest, map[string]string{
					"error": "provider not available — check credentials are wired and try again",
				})
				return
			}
			s.loop.SetProvider(p)
		}
		if err := s.settings.SetProvider(r.Context(), target); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, s.buildSettingsModelResponse(r.Context()))
	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// resolveModel returns the model id the next turn should run against.
// Empty string means "let the agent loop fall back to the provider's
// boot default" — the loop's Stream signature treats "" identically to
// no override. We don't surface DB errors here; a transient settings
// read failure shouldn't stall a turn when "use the default" is a
// perfectly safe answer.
func (s *Server) resolveModel(ctx context.Context) string {
	if s == nil || s.settings == nil {
		return ""
	}
	return s.settings.GetModel(ctx)
}
