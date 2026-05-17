package voyager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Calendar prep generator.
//
// Calendar events ingested from a connector (Google Cal, iCloud, …)
// arrive with title + location + time but no preparation context. The
// boss wants the agent to *anticipate* what needs doing - tickets for
// a concert, parking, dinner reservation, packing for a flight, etc.
//
// Per the design conversation: the prep schema is intentionally
// SCHEMA-FREE. We don't ship hardcoded templates ("if classification
// == concert, ask about parking"). Haiku is given the event details
// and asked to invent the right prep checklist for *this* event,
// drawing on whatever world knowledge fits. The structure we hand back
// to Studio is always the same shape - { label, rationale, done } -
// but the *content* is open-ended.
//
// Classification still happens because Studio's icon row uses it for
// glanceable scanning, but it's only a hint - out-of-vocabulary
// classifications are allowed.

// CalendarEvent is the local DTO. We don't import the dashboard package
// here to avoid an import cycle (dashboard already depends on basics).
type CalendarEvent struct {
	ID             uuid.UUID
	Title          string
	StartsAt       time.Time
	EndsAt         *time.Time
	Location       string
	Attendees      []string
	Classification string
	Prep           []PrepItem
}

type PrepItem struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Done      bool   `json:"done"`
	Rationale string `json:"rationale,omitempty"`
}

// GeneratePrepForEvent reads the event row, asks Haiku to classify it
// and draft a prep checklist, then writes the result back. Idempotent
// - running twice replaces the prep with the newer draft.
//
// When `m.llm == nil` (no LLM provider configured) the function inserts
// a single stub prep item flagging the gap rather than failing - keeps
// the calendar surface useful even on a low-config deploy.
func (m *Manager) GeneratePrepForEvent(ctx context.Context, eventID uuid.UUID) error {
	if m == nil || m.pool == nil {
		return errors.New("voyager: manager not configured")
	}

	// Pull the source event.
	var (
		title, location, classification string
		startsAt                        time.Time
		endsAt                          *time.Time
		attendeesRaw                    []byte
	)
	if err := m.pool.QueryRow(ctx, `
		SELECT title, location, classification, starts_at, ends_at,
		       COALESCE(attendees, '[]'::jsonb)
		FROM mem_calendar_events
		WHERE id = $1
	`, eventID).Scan(&title, &location, &classification, &startsAt, &endsAt, &attendeesRaw); err != nil {
		return fmt.Errorf("voyager: load calendar event %s: %w", eventID, err)
	}

	var attendees []string
	_ = json.Unmarshal(attendeesRaw, &attendees)

	// Build the draft.
	draft, err := m.draftPrep(ctx, prepDraftRequest{
		Title:          title,
		Location:       location,
		StartsAt:       startsAt,
		EndsAt:         endsAt,
		Attendees:      attendees,
		Classification: classification,
	})
	if err != nil {
		return err
	}

	// Persist classification + prep back to the event.
	prepJSON, _ := json.Marshal(draft.Prep)
	if _, err := m.pool.Exec(ctx, `
		UPDATE mem_calendar_events
		SET classification = COALESCE(NULLIF($2, ''), classification),
		    prep           = $3::jsonb,
		    updated_at     = NOW()
		WHERE id = $1
	`, eventID, draft.Classification, string(prepJSON)); err != nil {
		return fmt.Errorf("voyager: write prep: %w", err)
	}

	return nil
}

type prepDraftRequest struct {
	Title          string
	Location       string
	StartsAt       time.Time
	EndsAt         *time.Time
	Attendees      []string
	Classification string
}

type prepDraftResult struct {
	Classification string     `json:"classification"`
	Prep           []PrepItem `json:"prep"`
}

const calendarPrepSystem = `You are Jarvis, an always-on personal assistant for one user (the boss).

You're looking at an upcoming calendar event and your job is two things:

1. **Classify** the event type in one short lowercase word or phrase. Pick
   from common types if applicable: meeting, concert, flight, dinner,
   appointment, travel, social, personal, workout, doctor, dentist, call,
   demo, interview, wedding, funeral, conference, talk, recording, …
   You may invent a new classification if none fit - keep it short and
   lowercase. The boss explicitly asked that this NOT be schema-locked.

2. **Draft a prep checklist** - a short list of concrete things the boss
   should think about, do, or confirm before the event happens. Drawing
   on world knowledge specific to this event type and details.

   Examples by type (NOT a fixed schema - invent appropriate items):
   - concert → tickets bought · parking near venue · pre-show food · transit timing
   - flight → check-in opened · packed · ride to airport · OOO email · ID/passport
   - restaurant dinner → reservation made · dietary mentioned · address shared
   - doctor → insurance card · forms · fasting required · prior records
   - meeting → agenda · materials · prior context loaded · prep notes
   - wedding → RSVP · attire · gift · travel + lodging · childcare/pet care
   - travel → itinerary · accommodation · car rental · OOO

Each prep item gets:
   - "label": imperative phrase, ≤80 chars ("Book parking near MSG")
   - "rationale": one sentence (≤140 chars) explaining WHY this item - what
     the boss should think about. Reference specifics from the event when
     useful ("MSG-area lots fill by 6pm on event nights").

Return STRICT JSON exactly in this shape - no commentary, no markdown:

{
  "classification": "concert",
  "prep": [
    { "label": "...", "rationale": "..." },
    ...
  ]
}

Rules:
- 2 to 8 prep items. Avoid pointless ones - every item should be something
  the boss would forget if they didn't see it on the dashboard.
- Skip items only the agent can't help with (e.g. "have fun"). Focus on
  concrete actions or confirmations.
- "done" is omitted in your output - the system fills it in as false.
- No prep at all is okay for routine events (a recurring stand-up). Return
  { "classification": "...", "prep": [] }.`

func (m *Manager) draftPrep(ctx context.Context, req prepDraftRequest) (prepDraftResult, error) {
	if m.llm == nil {
		// LLM-less path: still return a useful classification + stub prep
		// so Studio doesn't render an empty card. The boss can re-run
		// once a provider is wired.
		return prepDraftResult{
			Classification: req.Classification,
			Prep: []PrepItem{
				{
					ID:        uuid.NewString(),
					Label:     "Review event details",
					Rationale: "LLM unavailable to draft prep; flag for manual review.",
					Done:      false,
				},
			},
		}, nil
	}

	prompt := buildPrepPrompt(req)
	raw, err := m.llm.Draft(ctx, "claude-haiku-4-5-20251001", calendarPrepSystem, prompt, 1200)
	if err != nil {
		return prepDraftResult{}, fmt.Errorf("haiku prep draft: %w", err)
	}
	// Haiku occasionally wraps JSON in ```json fences - strip them.
	raw = stripJSONFence(raw)

	var parsed prepDraftResult
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return prepDraftResult{}, fmt.Errorf("parse prep draft (raw=%q): %w", raw, err)
	}
	// Fill ids + done flags + truncate labels/rationale.
	for i := range parsed.Prep {
		if parsed.Prep[i].ID == "" {
			parsed.Prep[i].ID = uuid.NewString()
		}
		parsed.Prep[i].Done = false
		parsed.Prep[i].Label = truncStr(parsed.Prep[i].Label, 200)
		parsed.Prep[i].Rationale = truncStr(parsed.Prep[i].Rationale, 300)
	}
	if len(parsed.Prep) > 12 {
		parsed.Prep = parsed.Prep[:12]
	}
	parsed.Classification = strings.ToLower(strings.TrimSpace(parsed.Classification))
	return parsed, nil
}

func buildPrepPrompt(req prepDraftRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Event: %s\n", req.Title)
	fmt.Fprintf(&b, "Starts: %s\n", req.StartsAt.Format("Mon Jan 2 2006 at 3:04 PM MST"))
	if req.EndsAt != nil {
		fmt.Fprintf(&b, "Ends: %s\n", req.EndsAt.Format("Mon Jan 2 2006 at 3:04 PM MST"))
	}
	if req.Location != "" {
		fmt.Fprintf(&b, "Location: %s\n", req.Location)
	}
	if len(req.Attendees) > 0 {
		fmt.Fprintf(&b, "Attendees: %s\n", strings.Join(req.Attendees, ", "))
	}
	if req.Classification != "" && req.Classification != "other" {
		fmt.Fprintf(&b, "Suggested classification (you may keep or override): %s\n", req.Classification)
	}
	daysOut := time.Until(req.StartsAt).Hours() / 24
	fmt.Fprintf(&b, "Days until event: %.1f\n", daysOut)
	b.WriteString("\nDraft the classification + prep checklist for this event.")
	return b.String()
}

func stripJSONFence(s string) string {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "```") {
		// Drop the leading fence line.
		if i := strings.Index(t, "\n"); i >= 0 {
			t = t[i+1:]
		}
		// Drop the trailing fence.
		if i := strings.LastIndex(t, "```"); i >= 0 {
			t = t[:i]
		}
	}
	return strings.TrimSpace(t)
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
