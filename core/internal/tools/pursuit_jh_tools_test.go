package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dopesoft/infinity/core/internal/pursuits/jh"
)

// The pool is deliberately nil throughout: every guard asserted here has to
// fire BEFORE the store is reached, so a check that moved below the store call
// would panic instead of passing.

// A job_hunt pursuit is a pipeline, not a habit. Both descriptions have to say
// so and both have to name the check-in tool that must not be used on one -
// a habit tick filed against a pipeline records nothing and tells the boss
// nothing, and the model has no other way to know these are different shapes.
func TestPursuitJHToolsSayItIsAPipelineNotAHabit(t *testing.T) {
	for _, tool := range []interface{ Description() string }{
		&pursuitJHState{}, &pursuitJHWrite{},
	} {
		desc := strings.ToLower(tool.Description())
		for _, want := range []string{"pipeline", "not a habit", "pursuit_checkin"} {
			if !strings.Contains(desc, want) {
				t.Fatalf("%T description omits %q: %s", tool, want, desc)
			}
		}
	}
}

// A corpus entry is a question and the answer HE gave. An unattended turn
// (cron, heartbeat, sub-agent) has nobody who said any of it, so anything
// written would be invented and then shown back to him as his own words. The
// skill body says "only what he actually said", but prose is droppable by the
// model, so the refusal has to hold in code.
func TestPursuitJHWriteRefusesToBankAnAnswerOnAnUnattendedTurn(t *testing.T) {
	tool := &pursuitJHWrite{pool: nil}
	out, err := tool.Execute(WithAutonomous(context.Background()), map[string]any{
		"pursuit_id": "11111111-1111-1111-1111-111111111111",
		"action":     jh.ActionCorpus,
		"theme":      "leading through a reorg",
		"question":   "Tell me about a time you cut scope",
		"answer":     "something nobody said",
	})
	if err == nil {
		t.Fatalf("a corpus entry was banked on an unattended turn (returned %q)", out)
	}
	if out != "" {
		t.Fatalf("a refused write must return no result, got %q", out)
	}
	// The refusal has to read as a reason, not a category, and point at what to
	// do instead.
	if !strings.Contains(strings.ToLower(err.Error()), "live chat") {
		t.Fatalf("the refusal does not say where to raise it: %v", err)
	}
}

// The rest of the board is external fact - a posting exists, a recruiter has a
// title, a document was generated - and the nightly sweep that files roles IS
// an unattended turn. A blanket refusal copied from the coached pursuit would
// break the experience, so this asserts the guard is scoped to the corpus.
//
// The nil pool proves the call got through: it reaches the store and panics
// there rather than being turned away.
func TestPursuitJHWriteAllowsTheSweepToFileRoles(t *testing.T) {
	tool := &pursuitJHWrite{pool: nil}
	defer func() {
		if recover() == nil {
			t.Fatal("action=role never reached the store on an unattended turn")
		}
	}()
	_, _ = tool.Execute(WithAutonomous(context.Background()), map[string]any{
		"pursuit_id":  "11111111-1111-1111-1111-111111111111",
		"action":      jh.ActionRole,
		"company":     "Acme",
		"role_title":  "Head of Product",
		"source":      jh.SourceLinkedIn,
		"external_id": "ln-4417",
	})
}

// A value outside a column's vocabulary is refused with the accepted values
// named, before the store is reached. Same guarantee the HTTP cockpit gets,
// because both route through the same jh.Store.Apply.
func TestPursuitJHWriteRejectsAnInvalidEnumValue(t *testing.T) {
	tool := &pursuitJHWrite{pool: nil}
	_, err := tool.Execute(context.Background(), map[string]any{
		"pursuit_id": "11111111-1111-1111-1111-111111111111",
		"action":     jh.ActionRoleStage,
		"role_id":    "22222222-2222-2222-2222-222222222222",
		"stage":      "ghosted",
	})
	if err == nil {
		t.Fatal("an unknown stage was accepted")
	}
	for _, stage := range jh.RoleStages() {
		if !strings.Contains(err.Error(), stage) {
			t.Fatalf("rejection does not name the accepted stage %q: %v", stage, err)
		}
	}
}

// pursuit_id and action are what scope a write to one hunt and one operation.
// Neither may be defaulted: without the first the write would land on whichever
// board the store found, and without the second there is no operation to run.
func TestPursuitJHWriteRequiresItsScope(t *testing.T) {
	tool := &pursuitJHWrite{pool: nil}
	if _, err := tool.Execute(context.Background(), map[string]any{"action": jh.ActionRole}); err == nil ||
		!strings.Contains(err.Error(), "pursuit_id required") {
		t.Fatalf("err = %v, want it to name the missing pursuit_id", err)
	}
	if _, err := tool.Execute(context.Background(), map[string]any{"pursuit_id": "p"}); err == nil ||
		!strings.Contains(err.Error(), "action required") {
		t.Fatalf("err = %v, want it to name the missing action", err)
	}
	state := &pursuitJHState{pool: nil}
	if _, err := state.Execute(context.Background(), map[string]any{}); err == nil ||
		!strings.Contains(err.Error(), "pursuit_id required") {
		t.Fatalf("err = %v, want it to name the missing pursuit_id", err)
	}
}

// A salary band or a score the posting never stated must reach the store as
// absent, not as zero. Flattening them would put "$0" and "fit: 0" on a card as
// though the posting had said so, which is a wrong number rather than a missing
// one - and a wrong number is the kind he acts on.
func TestOptionalIntKeepsAbsentDistinctFromZero(t *testing.T) {
	in := map[string]any{"comp_min": float64(0), "fit_score": float64(72), "ghost_score": nil}

	if got := optionalInt(in, "comp_min"); got == nil || *got != 0 {
		t.Fatalf("comp_min = %v, want a stated zero to survive as zero", got)
	}
	if got := optionalInt(in, "fit_score"); got == nil || *got != 72 {
		t.Fatalf("fit_score = %v, want 72", got)
	}
	if got := optionalInt(in, "ghost_score"); got != nil {
		t.Fatalf("ghost_score = %v, want nil for an explicit null", got)
	}
	if got := optionalInt(in, "comp_max"); got != nil {
		t.Fatalf("comp_max = %v, want nil when the posting stated none", got)
	}
}

// The schema is what the model reads before it picks values. Every constrained
// field advertises its vocabulary from the same functions the writes are
// validated against, so the schema can never offer a value the store would
// reject, and adding a stage cannot leave the tool describing the old set.
func TestPursuitJHWriteSchemaAdvertisesTheRealVocabularies(t *testing.T) {
	schema := (&pursuitJHWrite{}).Schema()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema has no properties")
	}
	enumOf := func(field string) []string {
		prop, ok := props[field].(map[string]any)
		if !ok {
			t.Fatalf("schema has no %q field", field)
		}
		vals, ok := prop["enum"].([]string)
		if !ok {
			t.Fatalf("%q advertises no enum, so the model has to guess", field)
		}
		return vals
	}
	contains := func(haystack []string, needle string) bool {
		for _, v := range haystack {
			if v == needle {
				return true
			}
		}
		return false
	}

	for _, want := range jh.WriteActions() {
		if !contains(enumOf("action"), want) {
			t.Fatalf("action enum omits %q, so the model cannot reach it", want)
		}
	}
	for _, want := range jh.RoleStages() {
		if !contains(enumOf("stage"), want) {
			t.Fatalf("stage enum omits %q", want)
		}
	}
	for _, want := range jh.ArtifactKinds() {
		if !contains(enumOf("kind"), want) {
			t.Fatalf("kind enum omits %q", want)
		}
	}
	// source and status each serve two actions, so both vocabularies have to
	// be offered or one action becomes unreachable through the tool.
	for _, want := range append(append([]string{}, jh.RoleSources()...), jh.CorpusSources()...) {
		if !contains(enumOf("source"), want) {
			t.Fatalf("source enum omits %q", want)
		}
	}
	for _, want := range append(append([]string{}, jh.ContactStatuses()...), jh.ArtifactStatuses()...) {
		if !contains(enumOf("status"), want) {
			t.Fatalf("status enum omits %q", want)
		}
	}

	req, ok := schema["required"].([]string)
	if !ok || len(req) != 2 {
		t.Fatalf("required = %v, want exactly pursuit_id and action", schema["required"])
	}
	// The schema has to be encodable: it is marshalled into the tool
	// definition sent to the model, so a value that cannot round-trip would
	// take the whole tool out of reach at runtime rather than here.
	if _, err := json.Marshal(schema); err != nil {
		t.Fatalf("schema does not marshal: %v", err)
	}
}

// pursuit_jh_state is a pure read. The registry's name heuristic does not
// recognise the "_state" suffix, so without the explicit declaration this read
// would be bucketed with the mutating tools and gated like one.
func TestPursuitJHStateDeclaresItselfReadOnly(t *testing.T) {
	if !(&pursuitJHState{}).ReadOnly() {
		t.Fatal("pursuit_jh_state must declare ReadOnly, the name heuristic will not infer it")
	}
	if (&pursuitJHState{}).Name() != "pursuit_jh_state" {
		t.Fatalf("name = %q", (&pursuitJHState{}).Name())
	}
	if (&pursuitJHWrite{}).Name() != "pursuit_jh_write" {
		t.Fatalf("name = %q", (&pursuitJHWrite{}).Name())
	}
}

// A nil pool must leave these unregistered rather than registering tools that
// panic the moment the model picks one.
func TestRegisterPursuitJHToolsIsSafeWithoutAPool(t *testing.T) {
	r := NewRegistry()
	RegisterPursuitJHTools(r, nil)
	for _, name := range []string{"pursuit_jh_state", "pursuit_jh_write"} {
		if _, ok := r.Get(name); ok {
			t.Fatalf("%s registered without a database behind it", name)
		}
	}
	RegisterPursuitJHTools(nil, nil) // must not panic
}
