package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dopesoft/infinity/core/internal/vault"
)

// The wallet endpoints. Three of them, and what they refuse matters more than
// what they do.
//
//	GET  /api/wallet/cards            label, brand, last four. Never a number.
//	POST /api/wallet/cards            the ONLY way a card gets in.
//	POST /api/wallet/cards/revoke     retire one.
//
// There is deliberately NO endpoint that returns a card number, not even a
// masked or partial one, and no tool that reaches this either. A card is
// decrypted in exactly one place: inside the fill boundary that is about to
// type it into a checkout. Adding a "just for the settings page" read here
// would quietly undo the entire design, which is why this comment exists.

// walletCardIn is what the private card page posts. It arrives at this one
// route and never through chat, because a number in a message is a number in
// the transcript forever.
type walletCardIn struct {
	Token    string            `json:"token,omitempty"` // when adding from a one-time link
	Label    string            `json:"label"`
	Number   string            `json:"number"`
	CVC      string            `json:"cvc"`
	Name     string            `json:"name"`
	ExpMonth int               `json:"exp_month"`
	ExpYear  int               `json:"exp_year"`
	Billing  map[string]string `json:"billing"`
}

func (s *Server) handleWalletCards(w http.ResponseWriter, r *http.Request) {
	if s.vault == nil || !s.vault.Healthy() {
		// Say which knob is missing. "Unavailable" with no cause is how a
		// config problem gets mistaken for a product limitation.
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "the card vault is locked: set INFINITY_VAULT_KEY on core (32 random bytes, base64) and redeploy",
		})
		return
	}

	switch r.Method {
	case http.MethodGet:
		cards, err := s.vault.List(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if cards == nil {
			cards = []vault.Card{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"cards": cards})

	case http.MethodPost:
		var in walletCardIn
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		// A one-time link is single use and short lived, and redeeming it is
		// enforced in SQL so two simultaneous posts cannot both win.
		if strings.TrimSpace(in.Token) != "" {
			if err := s.vault.RedeemEnrollment(r.Context(), in.Token); err != nil {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
				return
			}
		}
		card, err := s.vault.Put(r.Context(), vault.PutCard{
			Label:    in.Label,
			PAN:      in.Number,
			CVC:      in.CVC,
			Name:     in.Name,
			ExpMonth: in.ExpMonth,
			ExpYear:  in.ExpYear,
			Billing:  in.Billing,
		})
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		// Only the clear half comes back, so even the page that just submitted
		// the number cannot read it again.
		writeJSON(w, http.StatusOK, map[string]any{"card": card})

	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleWalletRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.vault == nil || !s.vault.Healthy() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the card vault is locked"})
		return
	}
	var in struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if err := s.vault.Revoke(r.Context(), strings.TrimSpace(in.ID)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
