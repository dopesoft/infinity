// contacts_api.go — the boss's write access to his own phone book.
//
// The book learns by itself: a call teaches it, a web search teaches it, the
// agent saves what he hears. But an assistant who mishears a name once would
// have misheard it forever, because there was no way for the boss to correct
// anything. A book you cannot edit is a book that slowly fills with rubbish.
//
// Two routes, deliberately: save (create OR rename OR re-note, keyed on the
// number) and delete. Both go through the same contacts.Store the agent uses, so
// a contact the boss fixes by hand is instantly the contact Jarvis dials.
package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dopesoft/infinity/core/internal/contacts"
)

// POST /api/phone/contacts/save
//
// Creates or updates a contact. Keyed on the number, like every other write to
// the book, so saving the same number twice enriches rather than duplicates.
// Renaming keeps the number; changing the number of an existing person is a
// delete plus a save, which is what the UI does.
func (s *Server) handlePhoneContactSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.pool == nil {
		writeError(w, http.StatusServiceUnavailable, "no database")
		return
	}
	var in struct {
		Name     string   `json:"name"`
		Number   string   `json:"number"`
		Aliases  []string `json:"aliases"`
		Kind     string   `json:"kind"`
		Location string   `json:"location"`
		Note     string   `json:"note"`
		// Was is the number this contact had BEFORE the edit. Set when the boss
		// changed the number itself: the old row is removed so a corrected
		// number does not leave a ghost behind.
		Was string `json:"was"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	book := contacts.NewStore(s.pool)
	c := contacts.Contact{
		Name:     strings.TrimSpace(in.Name),
		Number:   strings.TrimSpace(in.Number),
		Aliases:  cleanStrings(in.Aliases),
		Kind:     strings.TrimSpace(in.Kind),
		Location: strings.TrimSpace(in.Location),
		Note:     strings.TrimSpace(in.Note),
		Source:   "boss", // the boss's own word outranks anything a call inferred
	}
	if err := book.Upsert(r.Context(), c); err != nil {
		// The store's errors are already written for a human ("929-310-0906 is
		// not a dialable number"), so pass them straight through.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// The number changed: drop the row it used to live under, or the book keeps
	// both and he gets asked "which Ariana?" forever.
	if was := contacts.NormalizeNumber(in.Was); was != "" && was != contacts.NormalizeNumber(in.Number) {
		if err := book.Delete(r.Context(), was); err != nil {
			writeError(w, http.StatusInternalServerError, "saved, but the old number could not be removed: "+err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/phone/contacts/delete
func (s *Server) handlePhoneContactDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.pool == nil {
		writeError(w, http.StatusServiceUnavailable, "no database")
		return
	}
	var in struct {
		Number string `json:"number"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	number := contacts.NormalizeNumber(in.Number)
	if number == "" {
		writeError(w, http.StatusBadRequest, "which number?")
		return
	}
	if err := contacts.NewStore(s.pool).Delete(r.Context(), number); err != nil {
		writeError(w, http.StatusInternalServerError, "delete contact")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func cleanStrings(in []string) []string {
	out := []string{}
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}
