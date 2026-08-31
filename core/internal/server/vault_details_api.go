package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dopesoft/infinity/core/internal/vault"
)

// /api/wallet/details — everything Jarvis knows about the boss personally.
//
//	GET   the whole catalog: what is saved, and what he may hand over.
//	POST  save a value, clear one, or flip a release switch.
//
// WHY THIS ENDPOINT EXISTS
//
// The name, address and identity details used to be plaintext rows in
// infinity_meta that the settings screen read and wrote straight over
// GET/POST /api/meta. Sealing the sensitive ones closed that route, which was
// the point, but it also left the screen editing a door that no longer opened.
// This is the replacement, and it is narrower in one specific way.
//
// WHAT IT WILL NOT DO
//
// Return a sealed value. A date of birth, an account number and the spoken
// password come back as "saved" or "not saved" and never as text, so the screen
// can be honest about state without the secret travelling to a browser, a log
// or a screenshot. The boss can replace one; he cannot re-read it. That is the
// trade sealing them WAS, and the alternative is serving his date of birth to
// anything holding a session, which is what used to happen.
//
// A name and an address are not sealed and do come back, because they are what
// you would write on an envelope and a form you cannot proofread is a form that
// stays wrong.

type detailsPost struct {
	// Values to store. An empty string clears that detail; a key that is not
	// mentioned is left alone, so saving one box cannot wipe the others.
	Values map[string]string `json:"values,omitempty"`
	// Release flips whether Jarvis may hand a detail over.
	Release map[string]bool `json:"release,omitempty"`
	// BillingSameAsShipping, when set, records the preference.
	BillingSameAsShipping *bool `json:"billing_same_as_shipping,omitempty"`
}

func (s *Server) handleVaultDetails(w http.ResponseWriter, r *http.Request) {
	if s.vault == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "the vault is not configured on this server",
		})
		return
	}
	details := vault.NewDetails(s.vault)

	switch r.Method {
	case http.MethodGet:
		list, same, err := details.All(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		cards, err := s.vault.List(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		// A locked vault is said out loud rather than rendered as "nothing
		// saved". The clear details still work without a key; the sealed ones
		// cannot be written or read, and the screen has to say which.
		writeJSON(w, http.StatusOK, map[string]any{
			"details":                  list,
			"billing_same_as_shipping": same,
			"sealed_available":         s.vault.Healthy(),
			"card_count":               len(cards),
		})

	case http.MethodPost:
		var in detailsPost
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		for key, value := range in.Values {
			spec, ok := vault.SpecFor(strings.TrimSpace(key))
			if !ok {
				writeJSON(w, http.StatusBadRequest, map[string]string{
					"error": "I do not keep a detail called " + key,
				})
				return
			}
			if spec.Sealed && !s.vault.Healthy() && strings.TrimSpace(value) != "" {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{
					"error": "the vault is locked, so I cannot store your " + strings.ToLower(spec.Label) +
						": set INFINITY_VAULT_KEY on core (32 random bytes, base64) and redeploy",
				})
				return
			}
			if err := details.Put(r.Context(), spec.Key, value); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}
		for key, on := range in.Release {
			if err := details.SetReleasable(r.Context(), strings.TrimSpace(key), on); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}
		if in.BillingSameAsShipping != nil {
			if err := details.SetBillingSameAsShipping(r.Context(), *in.BillingSameAsShipping); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
