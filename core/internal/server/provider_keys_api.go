package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// Provider keys API - GET / PUT / DELETE /api/settings/provider-keys.
//
// This is what makes a new brain a paste instead of a deploy. Before it,
// every vendor credential was an environment variable read once at boot, so
// "add DeepSeek" meant setting a Railway variable and waiting for a restart
// before the vendor picker stopped saying "not configured". A brain the boss
// cannot reach without a redeploy is a brain he does not have at the exact
// moment the one he is on runs out of usage.
//
// Generic by construction: the handler iterates llm.KeyableVendors and knows
// nothing about any particular vendor. Adding one is a row in that slice.
//
// The secret NEVER travels back out. Reads return a masked last-4 hint, which
// is enough to answer "which key is in there" and useless to anyone who
// intercepts it.

type providerKeyRow struct {
	Provider string `json:"provider"`
	// Configured means a usable credential exists from either source.
	Configured bool `json:"configured"`
	// Source is "ui" (pasted, stored in mem_provider_keys), "env" (the
	// deploy-time variable), or "" when nothing is set.
	Source string `json:"source"`
	// Hint is the masked tail of the stored key, "" for env-sourced keys
	// (the process env is not the UI's to display).
	Hint  string `json:"hint,omitempty"`
	Label string `json:"label,omitempty"`
	// EnvVar names the variable that serves as the fallback, so the UI can
	// say exactly what a deploy-time alternative would be called.
	EnvVar    string `json:"env_var"`
	UpdatedAt string `json:"updated_at,omitempty"`
	// Registered is the honest end state: this vendor is in the live
	// registry and selectable right now. A key can be stored and still not
	// registered if construction failed, and the UI must not claim
	// otherwise.
	Registered bool `json:"registered"`
	// Editable is false when there is no key store (no DB pool), so the UI
	// can explain that keys are read-only on this deployment instead of
	// offering a save button that cannot work.
	Editable bool `json:"editable"`
	// Implemented is false when Core carries no working client for this
	// vendor (a stub provider). The vendor still appears - hiding it would
	// just move the surprise - but the UI shows why it cannot be chosen
	// instead of offering a paste box that leads to a brain which fails
	// every turn.
	Implemented bool `json:"implemented"`
}

type providerKeysResponse struct {
	Providers []providerKeyRow `json:"providers"`
	// AvailableProviders mirrors the settings/model payload so the vendor
	// picker can refresh its enabled rows from this one response.
	AvailableProviders []string `json:"available_providers"`
	// Verified reports the outcome of the credential probe on a save:
	// "ok" (the vendor answered), "unsupported" (this vendor has no cheap
	// probe), or "unreachable" with Note carrying why. Absent on GET.
	Verified string `json:"verified,omitempty"`
	Note     string `json:"note,omitempty"`
}

func (s *Server) handleProviderKeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		resp, err := s.buildProviderKeysResponse(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	case http.MethodPut, http.MethodPost:
		s.saveProviderKey(w, r)
	case http.MethodDelete:
		s.deleteProviderKey(w, r)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// buildProviderKeysResponse reports one row per keyable vendor, whether or
// not it has a key, so the UI can render the complete list of places a key
// can be pasted rather than only the ones already filled in.
func (s *Server) buildProviderKeysResponse(ctx context.Context) (providerKeysResponse, error) {
	registered := map[string]bool{}
	available := []string{}
	if s.llmReg != nil {
		available = s.llmReg.Available()
		for _, id := range available {
			registered[id] = true
		}
	}
	stored := map[string]llm.StoredKey{}
	if s.llmKeys != nil {
		list, err := s.llmKeys.List(ctx)
		if err != nil {
			// Never render a lookup failure as "no keys stored" - that
			// would read as though the boss's credentials vanished.
			return providerKeysResponse{}, err
		}
		for _, k := range list {
			stored[k.Provider] = k
		}
	}

	out := providerKeysResponse{
		Providers:          make([]providerKeyRow, 0, len(llm.KeyableVendors)),
		AvailableProviders: available,
	}
	for _, v := range llm.KeyableVendors {
		// Constructing with an empty key is free and side-effect free: the
		// constructors only build a struct. It is the provider itself that
		// knows whether it is a stub, so nothing here needs a vendor list.
		implemented := llm.Implemented(v.New("", ""))
		row := providerKeyRow{
			Provider:    v.ID,
			EnvVar:      v.Env,
			Registered:  registered[v.ID],
			Editable:    s.llmKeys != nil && implemented,
			Implemented: implemented,
		}
		if k, ok := stored[v.ID]; ok {
			row.Configured = true
			row.Source = "ui"
			row.Hint = k.Hint
			row.Label = k.Label
			row.UpdatedAt = k.UpdatedAt.UTC().Format(time.RFC3339)
		} else if strings.TrimSpace(os.Getenv(v.Env)) != "" {
			row.Configured = true
			row.Source = "env"
		}
		out.Providers = append(out.Providers, row)
	}
	return out, nil
}

// saveProviderKey stores a pasted credential AND registers the provider in
// the live registry, so the vendor is selectable on the next turn without a
// restart. Storing without registering would be the built-but-not-wired
// failure: a key in a table that changes nothing until someone redeploys.
func (s *Server) saveProviderKey(w http.ResponseWriter, r *http.Request) {
	if s.llmKeys == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "I can't store keys on this deployment - there's no database pool attached, so the environment variable is the only route in.",
		})
		return
	}
	var body struct {
		Provider string `json:"provider"`
		APIKey   string `json:"api_key"`
		Label    string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	vendor, ok := llm.FindKeyableVendor(body.Provider)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "I don't know a vendor called " + body.Provider + ".",
		})
		return
	}
	// Refuse a credential for a brain that cannot answer. Taking the key,
	// storing it and showing the vendor as configured would be a lie that
	// only surfaces when a turn dies.
	if !llm.Implemented(vendor.New("", "")) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "I have no working client for " + vendor.ID + " yet, so a key would not get you a brain. Storing one would only look like it did.",
		})
		return
	}
	key := strings.TrimSpace(body.APIKey)
	if key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "That key came through empty - nothing to save.",
		})
		return
	}

	// Prove the credential before storing it. A key that the vendor turns
	// down is refused here, where the boss is looking at it, rather than
	// surfacing three hours later as a dead turn.
	built := vendor.New(key, llm.ModelForVendor(vendor.ID))
	verified, note := "unsupported", ""
	if v, ok := built.(llm.Verifier); ok {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		err := v.Verify(ctx)
		cancel()
		switch {
		case err == nil:
			verified = "ok"
		case errors.Is(err, llm.ErrKeyRejected):
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "That key didn't work - " + vendor.ID + " turned it down. Worth checking you copied the whole thing, and that the key is active on their dashboard.",
			})
			return
		default:
			// Could not reach the vendor. The key may well be fine, so
			// store it - but say plainly that it is unproven rather than
			// letting silence imply it was checked.
			verified, note = "unreachable", "I saved it, but I couldn't reach "+vendor.ID+" to prove it works: "+err.Error()
		}
	}

	if err := s.llmKeys.Set(r.Context(), vendor.ID, key, body.Label); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if s.llmReg != nil {
		s.llmReg.Register(built)
	}

	resp, err := s.buildProviderKeysResponse(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	resp.Verified, resp.Note = verified, note
	writeJSON(w, http.StatusOK, resp)
}

// deleteProviderKey removes a stored key and puts the registry back to what
// the environment alone supports - re-registering from the env var when one
// exists, dropping the vendor entirely when it doesn't. Leaving a live
// provider behind a deleted key would mean the picker offers a brain whose
// credential the boss just revoked.
func (s *Server) deleteProviderKey(w http.ResponseWriter, r *http.Request) {
	if s.llmKeys == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "No key store on this deployment - there's nothing stored here to remove.",
		})
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("provider"))
	if id == "" {
		var body struct {
			Provider string `json:"provider"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		id = strings.TrimSpace(body.Provider)
	}
	vendor, ok := llm.FindKeyableVendor(id)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "I don't know a vendor called " + id + ".",
		})
		return
	}
	if _, err := s.llmKeys.Delete(r.Context(), vendor.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if s.llmReg != nil {
		if envKey := strings.TrimSpace(os.Getenv(vendor.Env)); envKey != "" {
			s.llmReg.Register(vendor.New(envKey, llm.ModelForVendor(vendor.ID)))
		} else {
			s.llmReg.Unregister(vendor.ID)
		}
	}
	resp, err := s.buildProviderKeysResponse(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
