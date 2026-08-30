package dashboard

import (
	"context"
	"fmt"
	"time"

	"github.com/dopesoft/infinity/core/internal/mandate"
)

// mandateWorkItems turns the agent's open + finished-today Mandates into Agent
// Work board items — a mandate is a unit of agent work with a status, so it
// belongs on the same Kanban as plans/crons/skills, NOT on a separate card.
// Mapping (mirrors planWorkItems):
//   - open       -> Running      (with the passed/total criteria progress bar)
//   - verifying  -> Awaiting you (a crosscheck is in flight / required)
//   - done       -> Done today
//   - abandoned  -> Done today (dropped)
//
// Criteria ride inline so tapping the card opens the full definition-of-done +
// the verification verdict in ObjectViewer with no second fetch (exactly like a
// plan's step timeline).
func (a *API) mandateWorkItems(ctx context.Context) ([]WorkItem, error) {
	if a == nil || a.Pool == nil {
		return nil, nil
	}
	store := mandate.NewStore(a.Pool)
	// Active (open/verifying) regardless of age; terminal ones only if they
	// moved today (so the Done column reflects "today" like every other item).
	active, err := store.List(ctx, true, 40)
	if err != nil {
		return nil, err
	}
	recent, err := store.List(ctx, false, 40)
	if err != nil {
		return nil, err
	}

	startOfDay := time.Now().UTC().Truncate(24 * time.Hour)
	seen := map[string]bool{}
	out := make([]WorkItem, 0, len(active)+len(recent))

	add := func(m *mandate.Mandate) {
		if m == nil || seen[m.ID] {
			return
		}
		terminal := m.Status == mandate.StatusDone || m.Status == mandate.StatusAbandoned
		if terminal && m.UpdatedAt.Before(startOfDay) {
			return
		}
		seen[m.ID] = true

		passed := 0
		crit := make([]MandateCriterionDTO, 0, len(m.Criteria))
		for _, c := range m.Criteria {
			if c.Status == mandate.CritPass {
				passed++
			}
			crit = append(crit, MandateCriterionDTO{
				ID: c.ID, Text: c.Text, Status: c.Status, Evidence: c.Evidence,
			})
		}
		total := len(m.Criteria)

		column, sub := "running", ""
		switch m.Status {
		case mandate.StatusOpen:
			column = "running"
			sub = fmt.Sprintf("%d/%d verified", passed, total)
		case mandate.StatusVerifying:
			column = "awaiting"
			sub = "verifying"
		case mandate.StatusDone:
			column = "done"
			sub = "done"
		case mandate.StatusAbandoned:
			column = "done"
			sub = "abandoned"
		}

		doneCount := passed
		totalCount := total
		created := m.CreatedAt
		updated := m.UpdatedAt
		wi := WorkItem{
			ID:              "mandate-" + m.ID,
			Kind:            "mandate",
			Title:           m.Title,
			Subtitle:        sub,
			Engine:          "Mandate",
			Column:          column,
			SessionID:       m.SessionID,
			StartedAt:       &created,
			FinishedAt:      &updated,
			MandateCriteria: crit,
			// updated_at is this row's movement evidence: it moves when a step
			// passes or a criterion is verified, and not otherwise.
			LastMovedAt: &updated,
			DoneCount:   &doneCount,
			TotalCount:  &totalCount,
			Instruction: m.Summary,
		}
		if len(m.Crosscheck) > 0 {
			wi.Crosscheck = m.Crosscheck
		}
		out = append(out, wi)
	}

	for _, m := range active {
		add(m)
	}
	for _, m := range recent {
		add(m)
	}
	return out, nil
}
