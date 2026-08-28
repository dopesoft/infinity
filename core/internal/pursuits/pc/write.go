package pc

import (
	"context"
	"errors"
	"strings"
)

// WriteRequest is the union of every cockpit mutation payload. One envelope so
// the HTTP API, the agent tools, and any future caller all speak the same
// shape and inherit the same validation.
type WriteRequest struct {
	// identity
	Identity     string        `json:"identity"`
	Objective    string        `json:"objective"`
	Pattern      string        `json:"pattern"`
	PressureTest *PressureTest `json:"pressure_test"`
	Timezone     string        `json:"timezone"`

	// session
	Kind      string         `json:"kind"`
	Answers   map[string]any `json:"answers"`
	CoachNote string         `json:"coach_note"`

	// proof
	ProofID   string  `json:"proof_id"`
	Label     string  `json:"label"`
	Taken     *bool   `json:"taken"`
	Note      string  `json:"note"`
	SessionID *string `json:"session_id"`

	// evidence / memory / pattern
	Body   string   `json:"body"`
	Title  string   `json:"title"`
	Tags   []string `json:"tags"`
	Weight *int     `json:"weight"`

	// review
	Wins          string         `json:"wins"`
	Misses        string         `json:"misses"`
	NextIdentity  string         `json:"next_identity"`
	NextObjective string         `json:"next_objective"`
	NextPattern   string         `json:"next_pattern"`
	Adjustments   map[string]any `json:"adjustments"`
}

// Write actions. These double as the HTTP path suffixes under
// /api/pursuits/pc/ and as the `action` enum on the pursuit_pc_write tool.
const (
	ActionIdentity   = "identity"
	ActionSession    = "session"
	ActionProof      = "proof"
	ActionProofTaken = "proof/taken"
	ActionEvidence   = "evidence"
	ActionMemory     = "memory"
	ActionPattern    = "pattern"
	ActionReview     = "review"
)

// WriteActions enumerates every accepted action, for schema generation and
// validation.
func WriteActions() []string {
	return []string{
		ActionIdentity, ActionSession, ActionProof, ActionProofTaken,
		ActionEvidence, ActionMemory, ActionPattern, ActionReview,
	}
}

// IsWriteAction reports whether the supplied action is one Apply handles.
func IsWriteAction(action string) bool {
	for _, a := range WriteActions() {
		if a == action {
			return true
		}
	}
	return false
}

// ErrUnknownAction is returned for an action outside WriteActions.
var ErrUnknownAction = errors.New("unknown pursuit_pc action")

// Apply is the SINGLE write chokepoint for the Psycho-Cybernetics experience.
//
// Rule #1c: every caller (HTTP cockpit, agent tool, any future one) routes
// through here, so the derived side effects below are guaranteed by
// construction rather than by each caller remembering them:
//
//   - a morning pledge always becomes a tracked proof row
//   - an evening correction always lands in the pattern history
//   - a captured evidence/resistance answer always becomes an evidence row
//   - an identity edit always records the framing change
//
// Those are mechanics, so they live in code and cannot be dropped by an LLM
// that forgot a step (Rule #1b), and the cockpit cannot drift from chat by
// re-implementing half of them client-side.
func (s *Store) Apply(ctx context.Context, action, pursuitID string, req WriteRequest) error {
	// Unknown action first, so a typo reads as a typo rather than as whatever
	// the pursuit lookup happens to say, and costs no database round trip.
	if !IsWriteAction(action) {
		return ErrUnknownAction
	}
	// Reject an ordinary pursuit before any write lands, uniformly for every
	// action. Ordinary pursuits cannot be mutated through this surface.
	if _, err := s.Header(ctx, pursuitID); err != nil {
		return err
	}

	switch action {
	case ActionIdentity:
		if _, err := s.SaveIdentity(ctx, pursuitID, req.Identity, req.Objective,
			req.Pattern, req.PressureTest, req.Timezone); err != nil {
			return err
		}
		if strings.TrimSpace(req.Pattern) != "" {
			if _, err := s.AddPattern(ctx, pursuitID, PatternLimiting, req.Pattern, nil); err != nil {
				return err
			}
		}
		if strings.TrimSpace(req.Identity) != "" {
			if _, err := s.AddPattern(ctx, pursuitID, PatternOperating, req.Identity, nil); err != nil {
				return err
			}
		}
		return nil

	case ActionSession:
		sess, err := s.LogSession(ctx, pursuitID, req.Kind, req.Answers, req.CoachNote)
		if err != nil {
			return err
		}
		// The state as it stands AFTER the session is logged. LogSession
		// refreshes the derived day but touches neither the identity nor the
		// cycle, so this is the state the two promotions below reason about.
		st, err := s.loadState(ctx, pursuitID)
		if err != nil {
			return err
		}
		// An onboarding session has to LEAVE the programme with an identity.
		// The cockpit walks the boss through a separate identity write first,
		// but a coaching chat naturally logs the whole thing as one onboarding
		// session - and without this that answer would land in an untyped blob,
		// the state would stay blank, and NextGuidance would open onboarding
		// again on the next read. He would answer the same three questions
		// forever and never see day 1. Mechanic, so it lives here rather than
		// in the skill's prose (Rule #1b).
		//
		// Only fields still blank are promoted, so the cockpit's two-step write
		// is not overwritten and does not file a second copy of the pattern.
		if req.Kind == SessionOnboarding {
			if err := s.promoteOnboarding(ctx, pursuitID, st, req.Answers); err != nil {
				return err
			}
		}
		if pledge := answerString(req.Answers, "proof_pledge"); pledge != "" {
			if _, err := s.AddProof(ctx, pursuitID, pledge, &sess.ID); err != nil {
				return err
			}
		}
		if correction := answerString(req.Answers, "correction"); correction != "" {
			if _, err := s.AddPattern(ctx, pursuitID, PatternCorrection, correction,
				map[string]any{"session_id": sess.ID}); err != nil {
				return err
			}
		}
		// A capture written into a session's answers is still a capture. Filing
		// it here means the midday flow behaves the same whether it arrived from
		// the cockpit form or from Jarvis logging the session out of a chat.
		for _, capture := range sessionCaptureKeys {
			body := answerString(req.Answers, capture.Key)
			if body == "" {
				continue
			}
			if _, err := s.AddEvidence(ctx, pursuitID, capture.Kind, body, nil, &sess.ID); err != nil {
				return err
			}
		}
		// A review session must actually CLOSE the cycle, and does so LAST:
		// everything above is filed against the cycle being reviewed, and
		// CompleteReview is what rolls the counter forward. Closing first would
		// attribute the closing session's own captures to the new cycle.
		//
		// Gated on the cycle being due so the cockpit's dedicated review write
		// followed by a logged review session cannot advance the programme
		// twice: the first close resets current_day to 1, which shuts this
		// branch. Losing 21 days to a double-roll is not something the boss
		// could recover by hand.
		if req.Kind == SessionReview && ReviewDue(st) {
			if _, err := s.CompleteReview(ctx, pursuitID,
				answerString(req.Answers, "wins"),
				answerString(req.Answers, "misses"),
				answerString(req.Answers, "next_identity"),
				answerString(req.Answers, "next_objective"),
				answerString(req.Answers, "next_pattern"),
				nil); err != nil {
				return err
			}
		}
		return nil

	case ActionProof:
		_, err := s.AddProof(ctx, pursuitID, req.Label, req.SessionID)
		return err

	case ActionProofTaken:
		if strings.TrimSpace(req.ProofID) == "" {
			return errors.New("proof_id required")
		}
		taken := true
		if req.Taken != nil {
			taken = *req.Taken
		}
		_, err := s.SetProofTaken(ctx, pursuitID, req.ProofID, taken, req.Note)
		return err

	case ActionEvidence:
		kind := strings.TrimSpace(req.Kind)
		if kind == "" {
			kind = EvidenceEvidence
		}
		_, err := s.AddEvidence(ctx, pursuitID, kind, req.Body, req.Tags, req.SessionID)
		return err

	case ActionMemory:
		weight := 50
		if req.Weight != nil {
			weight = *req.Weight
		}
		_, err := s.AddMemory(ctx, pursuitID, req.Title, req.Body, req.Tags, weight)
		return err

	case ActionPattern:
		kind := strings.TrimSpace(req.Kind)
		if kind == "" {
			kind = PatternLimiting
		}
		_, err := s.AddPattern(ctx, pursuitID, kind, req.Body, nil)
		return err

	case ActionReview:
		_, err := s.CompleteReview(ctx, pursuitID, req.Wins, req.Misses,
			req.NextIdentity, req.NextObjective, req.NextPattern, req.Adjustments)
		return err

	default:
		return ErrUnknownAction
	}
}

// onboardingAnswerKeys maps each field the onboarding phase establishes onto
// the answer keys that may carry it.
//
// Two spellings per field on purpose. The cockpit form keys off the prompt keys
// in coach.go ("limiting_pattern"), while a coaching chat keys off the tool
// schema, which names the same field "pattern" for action='identity'. Both are
// the boss answering the same question, so both have to land.
var onboardingAnswerKeys = struct {
	Identity  []string
	Objective []string
	Pattern   []string
}{
	Identity:  []string{"identity", "operating_identity"},
	Objective: []string{"objective", "abundance_objective"},
	Pattern:   []string{"limiting_pattern", "pattern"},
}

// promoteOnboarding lifts an onboarding session's answers into the programme
// state, filling ONLY the fields that are still blank.
//
// Blank-only is what makes it safe to run alongside the cockpit, which writes
// the identity through action='identity' before logging the session: by the
// time the session lands there is nothing left to fill, so this is a no-op and
// the pattern history does not get a duplicate row.
func (s *Store) promoteOnboarding(ctx context.Context, pursuitID string, st State, answers map[string]any) error {
	// Nothing to promote once the programme is past onboarding by the same
	// definition NextGuidance uses. Reading it through NeedsOnboarding rather
	// than re-testing the fields here is what keeps the two in step.
	if !NeedsOnboarding(st) {
		return nil
	}
	identity, objective, pattern := "", "", ""
	if strings.TrimSpace(st.CurrentIdentity) == "" {
		identity = firstAnswer(answers, onboardingAnswerKeys.Identity)
	}
	if strings.TrimSpace(st.CurrentObjective) == "" {
		objective = firstAnswer(answers, onboardingAnswerKeys.Objective)
	}
	if strings.TrimSpace(st.CurrentLimitingPattern) == "" {
		pattern = firstAnswer(answers, onboardingAnswerKeys.Pattern)
	}

	// The pressure test only rides along when the state carries none, for the
	// same reason: the cockpit writes it with the identity.
	var pressure *PressureTest
	if strings.TrimSpace(st.PressureTest.Fear+st.PressureTest.Doubt+st.PressureTest.Alternate) == "" {
		pt := PressureTest{
			Fear:      answerString(answers, "pressure_fear"),
			Doubt:     answerString(answers, "pressure_doubt"),
			Alternate: answerString(answers, "pressure_alternate"),
		}
		if strings.TrimSpace(pt.Fear+pt.Doubt+pt.Alternate) != "" {
			pressure = &pt
		}
	}

	if identity == "" && objective == "" && pattern == "" && pressure == nil {
		return nil
	}
	if _, err := s.SaveIdentity(ctx, pursuitID, identity, objective, pattern, pressure, ""); err != nil {
		return err
	}
	// Same history rows action='identity' files, so the pattern list reads the
	// same whether onboarding arrived from the cockpit or from a conversation.
	if pattern != "" {
		if _, err := s.AddPattern(ctx, pursuitID, PatternLimiting, pattern, nil); err != nil {
			return err
		}
	}
	if identity != "" {
		if _, err := s.AddPattern(ctx, pursuitID, PatternOperating, identity, nil); err != nil {
			return err
		}
	}
	return nil
}

// firstAnswer returns the first non-empty answer among the supplied keys.
func firstAnswer(answers map[string]any, keys []string) string {
	for _, k := range keys {
		if v := answerString(answers, k); v != "" {
			return v
		}
	}
	return ""
}

// sessionCaptureKeys maps the answer keys that carry a daytime capture onto
// the evidence kind they file as. Iterated in a fixed order so a session with
// both keys always writes evidence before resistance.
var sessionCaptureKeys = []struct {
	Key  string
	Kind string
}{
	{Key: "evidence", Kind: EvidenceEvidence},
	{Key: "resistance", Kind: EvidenceResistance},
}

// answerString pulls a trimmed string out of the freeform answers blob.
func answerString(answers map[string]any, key string) string {
	if answers == nil {
		return ""
	}
	if v, ok := answers[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}
