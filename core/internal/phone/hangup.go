// hangup.go — who is allowed to end the call, and when.
//
// Two failures on Jul 13, one call apart:
//
//   - The boss verified, gave his ask, and the agent said "let me take a moment
//     to shape something tender" and then HUNG UP ON HIM. It had been told its
//     hands go to work the moment it hangs up, so it hung up to get to work.
//   - An unverified call was cut mid-sentence the same way.
//
// The model (a mini realtime model) reaches for hangup_call far too eagerly, and
// a dropped call is the rudest thing this line can do. Politeness is judgment,
// but "do not hang up while the human is still talking" is a MECHANIC, so it
// lives here in code where the model cannot forget it.
//
// The detection also had to be hardened: it used to substring-match the raw
// event for "hangup_call", and the persona notes we now inject into the live
// call CONTAIN the literal text "hangup_call". The agent's own briefing, echoed
// back by the server, would have hung up the call.
package phone

import (
	"encoding/json"
	"strings"
	"time"
)

// deadAirHangup: if the human has not made a sound in this long, the call is
// abandoned and the agent may end it without a goodbye.
const deadAirHangup = 45 * time.Second

// farewells are the words that mean "this call is over". Kept deliberately
// tight: a false positive drops a live call (the bug), while a false negative
// only means Jarvis waits and the human hangs up first, which costs nothing.
//
// "Cheers" is NOT here on purpose. Jarvis is British and says it to mean thanks;
// on Jul 13 he said "Cheers, let me acknowledge that and then we can get started
// with your request" and then hung up.
var farewells = []string{
	"goodbye", "good bye", "bye now", "bye bye", "bye.", "bye!",
	"take care", "talk soon", "speak soon", "talk to you later",
	"have a good", "have a great", "have a lovely", "farewell",
	"that's all", "that is all", "that'll be all", "that will be all",
	"that's everything", "that is everything", "nothing else", "we're done",
	"i'm done", "that's it for now", "good day to you", "good night",
}

// saidFarewell reports whether a spoken line closes the call.
func saidFarewell(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return false
	}
	if t == "bye" || t == "goodbye" {
		return true
	}
	for _, f := range farewells {
		if strings.Contains(t, f) {
			return true
		}
	}
	return false
}

// hangupAllowed decides whether the agent may end the call right now.
//
// Permitted when either side has actually said goodbye, when the human has gone
// silent long enough that the call is abandoned, or when they never spoke at
// all. Otherwise the human is still mid-conversation and the answer is no: an
// agent that hangs up on the boss while he is giving instructions is worse than
// an agent that stays on the line a few seconds too long.
func hangupAllowed(lastHumanText, lastAgentText string, lastHumanAt, now time.Time) bool {
	if saidFarewell(lastHumanText) || saidFarewell(lastAgentText) {
		return true
	}
	if lastHumanAt.IsZero() {
		return true // nobody ever spoke: silence, a robocall, a wrong number
	}
	return now.Sub(lastHumanAt) >= deadAirHangup
}

// realtimeEvent is the lenient shape of any realtime event that could carry a
// FUNCTION CALL, across the shapes the API uses (response.output_item.*,
// response.done, conversation.item.created).
type realtimeEvent struct {
	Type string       `json:"type"`
	Item functionItem `json:"item"`
	Response struct {
		Output []functionItem `json:"output"`
	} `json:"response"`
}

type functionItem struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	CallID    string `json:"call_id"`
	Arguments string `json:"arguments"` // a JSON string, per the realtime API
}

// functionCallArg finds an invocation of the named function and pulls one
// argument out of it, returning the call_id the result must be posted back
// against. The same invocation is announced on more than one event
// (response.output_item.done and response.done both carry it), so callers
// dedupe on the returned call_id.
func functionCallArg(raw []byte, fn, arg string) (callID, value string, ok bool) {
	var ev realtimeEvent
	if json.Unmarshal(raw, &ev) != nil {
		return "", "", false
	}
	items := append([]functionItem{ev.Item}, ev.Response.Output...)
	for _, it := range items {
		if it.Type != "function_call" || it.Name != fn || it.Arguments == "" {
			continue
		}
		var args map[string]any
		if json.Unmarshal([]byte(it.Arguments), &args) != nil {
			continue
		}
		v, _ := args[arg].(string)
		if strings.TrimSpace(v) == "" {
			continue
		}
		return it.CallID, strings.TrimSpace(v), true
	}
	return "", "", false
}

// functionCalled returns the names of functions the model actually INVOKED in
// this event.
//
// It requires the item's type to be "function_call" — it does not scan the raw
// bytes for a name. That distinction is load-bearing: our own injected system
// notes quote "hangup_call" in their text, and a substring match would treat the
// briefing as a request to end the call.
func functionCalled(raw []byte) []string {
	var ev realtimeEvent
	if json.Unmarshal(raw, &ev) != nil {
		return nil
	}
	var names []string
	for _, it := range append([]functionItem{ev.Item}, ev.Response.Output...) {
		if it.Type == "function_call" && it.Name != "" {
			names = append(names, it.Name)
		}
	}
	return names
}

// calledFunction reports whether the event invoked the named function.
func calledFunction(raw []byte, name string) bool {
	for _, got := range functionCalled(raw) {
		if got == name {
			return true
		}
	}
	return false
}
