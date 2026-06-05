package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dopesoft/infinity/core/internal/compass"
)

// compassSectionDTO is one authored Compass section.
type compassSectionDTO struct {
	Section  string `json:"section"`
	Label    string `json:"label"`
	Content  string `json:"content"`
	Position int    `json:"position"`
}

// /api/compass
//
//	GET → the boss's authored Compass. Always returns the canonical five
//	      sections (mission/goals/challenges/principles/fronts) in order,
//	      with stored content merged in, so the Studio editor renders every
//	      field even before anything is written.
//	PUT → upsert one section: { section, content, position? }.
//
// The Compass is injected into every turn by compass.Provider. The agent reads
// it; only the boss writes it (this endpoint).
func (s *Server) handleCompass(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no database pool"})
		return
	}
	store := compass.NewStore(s.pool)

	switch r.Method {
	case http.MethodGet:
		stored, err := store.List(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		byKey := make(map[string]compass.Section, len(stored))
		for _, sec := range stored {
			byKey[sec.Section] = sec
		}
		// Canonical five, in order, with stored content merged. Extra
		// (non-canonical) stored sections append after, preserving the boss's
		// ability to add his own.
		out := make([]compassSectionDTO, 0, len(compass.Sections)+len(stored))
		seen := map[string]bool{}
		for i, key := range compass.Sections {
			seen[key] = true
			d := compassSectionDTO{Section: key, Label: compass.SectionLabel[key], Position: i}
			if sec, ok := byKey[key]; ok {
				d.Content = sec.Content
				d.Position = sec.Position
			}
			out = append(out, d)
		}
		for _, sec := range stored {
			if seen[sec.Section] {
				continue
			}
			out = append(out, compassSectionDTO{
				Section:  sec.Section,
				Label:    sec.Section,
				Content:  sec.Content,
				Position: sec.Position,
			})
		}
		writeJSON(w, http.StatusOK, out)

	case http.MethodPut, http.MethodPost:
		var in compassSectionDTO
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		in.Section = strings.ToLower(strings.TrimSpace(in.Section))
		if in.Section == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "section required"})
			return
		}
		if err := store.Upsert(r.Context(), in.Section, in.Content, in.Position); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})

	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
