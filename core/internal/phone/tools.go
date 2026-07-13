// tools.go — the agent-facing verb: phone_call.
//
// One generic tool, one brief contract. The agent writes a producer-style
// brief (objective, shareable facts, hard limits), the tool stores it under
// mem_agent_state and asks Twilio to dial the callee and bridge the leg to
// OpenAI's SIP endpoint with the brief id riding an X-Jarvis-Brief header.
// The webhook side (webhook.go) correlates the leg back to the brief and
// runs the call. Trust-gating happens in proactive.PhoneGate, not here.
package phone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/contacts"
	"github.com/dopesoft/infinity/core/internal/tools"
	"github.com/google/uuid"
)

// twilioCallsURL is Twilio's call-creation endpoint (account SID splices in).
const twilioCallsURL = "https://api.twilio.com/2010-04-01/Accounts/%s/Calls.json"

// e164 is the strict callee format Twilio expects.
var e164 = regexp.MustCompile(`^\+[1-9]\d{6,14}$`)

// RegisterPhoneTools wires the phone_call verb into the shared registry.
func RegisterPhoneTools(reg *tools.Registry, m *Manager) {
	if reg == nil || m == nil {
		return
	}
	reg.Register(&phoneCall{m: m})
}

type phoneCall struct{ m *Manager }

func (t *phoneCall) Name() string { return "phone_call" }

func (t *phoneCall) Description() string {
	return "Place a real phone call on the boss's behalf. A separate voice agent " +
		"(Jarvis's phone persona) conducts the call and knows ONLY what you put in " +
		"the brief - so write the goal like a producer briefing a field agent: the " +
		"exact objective, the facts it may share, fallback positions, and what it " +
		"must NOT commit to (especially money). Every call needs the boss's approval " +
		"via the Trust queue before it dials. When the call ends, the transcript and " +
		"outcome land on the dashboard (surface 'calls') - read it and report back."
}

func (t *phoneCall) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"to": map[string]any{
				"type": "string",
				"description": "Who to call: either a number in E.164 format (+14155550123), or a NAME already " +
					"in the boss's phone book (\"Ariana\", \"Goodfellas Pizza\"), which is resolved for you. " +
					"If the name is unknown or matches more than one contact this fails and says so - then " +
					"find the number (web search, for a business), confirm the right one with the boss, and " +
					"contact_save it before calling.",
			},
			"location": map[string]any{
				"type":        "string",
				"description": "For a business, which branch: \"the one on Preston Road\". Saved to the phone book so the boss never has to disambiguate twice.",
			},
			"contact_kind": map[string]any{
				"type":        "string",
				"enum":        []any{"person", "org"},
				"description": "Is this a person (cousin, friend) or an organization (business, restaurant, office)? Sets the contact-book icon.",
			},
			"topic": map[string]any{
				"type":        "string",
				"description": "3-5 word label for what this call is about, e.g. \"pizza pickup order\" or \"congratulating cousin Lerato\". Shown as the call's subtitle on the dashboard.",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Who you're calling - the business or person's name (e.g. \"Goodfellas Pizza\"). Stamped into the call history so future calls with this number know the relationship by name.",
			},
			"goal": map[string]any{
				"type": "string",
				"description": "The full brief: exact objective, facts the call agent may share, " +
					"fallback positions, and hard limits. The call agent knows nothing else.",
			},
			"constraints": map[string]any{
				"type":        "string",
				"description": "Optional hard boundaries, e.g. spend ceiling, topics to avoid, when to bail out.",
			},
			"at": map[string]any{
				"type":        "string",
				"description": "Optional RFC3339 time to place this call LATER (e.g. 2026-07-11T15:00:00-05:00). Omit to call now. Scheduled calls are snapped into 8am-9pm boss-local hours.",
			},
			"include_identity": map[string]any{
				"type":        "boolean",
				"description": "Set true ONLY when the call requires verifying Mr. Kai's identity (a bank, utility, doctor) AND the boss's errand authorizes it. The stored details are attached server-side; you never see them.",
			},
			"include_payment": map[string]any{
				"type": "boolean",
				"description": "Set true ONLY when the boss's errand requires paying over the phone AND " +
					"paying on arrival/pickup isn't an option. The stored card is attached to the brief " +
					"server-side - you never see the number. Prefer 'pay when Kai arrives' whenever the " +
					"merchant allows it.",
			},
		},
		"required": []string{"to", "goal"},
	}
}

func (t *phoneCall) Execute(ctx context.Context, in map[string]any) (string, error) {
	m := t.m
	if missing := m.MissingOutboundEnvs(); len(missing) > 0 {
		return "", fmt.Errorf("phone_call is not configured yet - these env vars are missing on core: %s. "+
			"Tell the boss so he can set them", strings.Join(missing, ", "))
	}

	to := strings.TrimSpace(str(in, "to"))
	goal := strings.TrimSpace(str(in, "goal"))
	constraints := strings.TrimSpace(str(in, "constraints"))
	name := strings.TrimSpace(str(in, "name"))

	// "call Ariana" is the whole point of a phone book, so a NAME here is
	// resolved rather than rejected. Ambiguity is never guessed at: two
	// Goodfellas means the boss gets asked which one, because dialing the wrong
	// branch is worse than asking.
	if !e164.MatchString(to) && to != "" && m.book != nil {
		found, err := m.book.Resolve(ctx, to)
		if err != nil {
			return "", fmt.Errorf("looking up %q in the phone book failed: %w", to, err)
		}
		switch len(found) {
		case 0:
			return "", fmt.Errorf("%q is not in the boss's phone book, so I have no number for them. "+
				"Find the number first (web_search for a business), confirm with the boss which one he means "+
				"if there is any doubt, contact_save it, then call", to)
		case 1:
			if name == "" {
				name = found[0].Name
			}
			to = found[0].Number
		default:
			return "", fmt.Errorf("%q matches more than one contact, so I will not guess. Ask the boss which he means, then call that number directly:\n%s",
				to, contacts.Describe(found))
		}
	}
	if !e164.MatchString(to) {
		return "", fmt.Errorf("'to' must be E.164 (e.g. +14155550123) or a name in the phone book; got %q", to)
	}
	if goal == "" {
		return "", errors.New("'goal' is required - the call agent knows only what the brief says")
	}

	// Payment vault: when the errand requires paying by phone, the card is
	// fetched from infinity_meta and appended to the brief HERE, server-side
	// — the main agent's context never contains the number, only this
	// boolean. The field agent reads it to the merchant; the monitor scrubs
	// card digits from the stored transcript.
	if b, _ := in["include_payment"].(bool); b {
		card, err := m.vaultPaymentCard(ctx)
		if err != nil {
			return "", err
		}
		constraints = strings.TrimSpace(constraints + "\nPayment (authorized by the boss for THIS errand only - never volunteer it unprompted, only to complete the agreed purchase): " + card)
	}
	if b, _ := in["include_identity"].(bool); b {
		id, err := m.vaultIdentity(ctx)
		if err != nil {
			return "", err
		}
		constraints = strings.TrimSpace(constraints + "\nIdentity verification (authorized for THIS errand only - give only the specific detail they ask for, never volunteer the rest): " + id)
	}

	// Store the brief FIRST: the incoming-call webhook rehydrates it by id,
	// so it must exist before Twilio bridges the leg.
	briefID := uuid.NewString()
	kind := strings.TrimSpace(str(in, "contact_kind"))
	location := strings.TrimSpace(str(in, "location"))
	brief := &Brief{
		Topic:       strings.TrimSpace(str(in, "topic")),
		Kind:        kind,
		Name:        name,
		To:          to,
		Goal:        goal,
		Constraints: constraints,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	// Every call teaches the phone book. The boss should never have to say
	// "save that number": he told Jarvis who he was calling once, and from then
	// on the name is enough. Deterministic, so it cannot be forgotten the way a
	// sentence in a prompt can.
	if m.book != nil && name != "" {
		if err := m.book.Upsert(ctx, contacts.Contact{
			Name: name, Number: to, Kind: kind, Location: location, Source: "call",
		}); err != nil {
			// Not fatal: the call still goes through. Loud, because a book that
			// silently stops learning would send us right back to reciting digits.
			log.Printf("phone: saving %s (%s) to the phone book failed: %v", name, to, err)
		}
		m.book.MarkCalled(ctx, to)
	}
	// Scheduled path: stash the whole brief in the one-shot table and let the
	// poller dial at the target time (snapped into calling hours). Reuses the
	// identical brief/dial path, just later.
	if atStr := strings.TrimSpace(str(in, "at")); atStr != "" {
		at, perr := time.Parse(time.RFC3339, atStr)
		if perr != nil {
			return "", fmt.Errorf("'at' must be RFC3339 (e.g. 2026-07-11T15:00:00-05:00); got %q", atStr)
		}
		fireAt, serr := m.ScheduleCall(ctx, brief, at, "scheduled by agent")
		if serr != nil {
			return "", fmt.Errorf("could not schedule the call: %w", serr)
		}
		out, _ := json.Marshal(map[string]any{
			"ok": true, "scheduled_for": fireAt.Format(time.RFC3339),
			"note": "Call scheduled - I'll place it at " + fireAt.In(bossLocation()).Format("Jan 2 3:04pm") + " and report the outcome.",
		})
		return string(out), nil
	}

	if err := m.storeBrief(ctx, briefID, brief); err != nil {
		return "", fmt.Errorf("could not store the call brief: %w", err)
	}

	callSID, err := m.createTwilioCall(ctx, to, briefID)
	if err != nil {
		return "", err
	}
	infoLog.Printf("phone: outbound call placed to %s (twilio sid=%s brief=%s)", to, callSID, briefID)

	out, _ := json.Marshal(map[string]any{
		"ok":       true,
		"call_sid": callSID,
		"brief_id": briefID,
		"note":     "Jarvis is on the line - the transcript and outcome will land on the dashboard when the call ends.",
	})
	return string(out), nil
}

// createTwilioCall asks Twilio to dial the callee and bridge to the OpenAI
// SIP URI; the X-Jarvis-Brief SIP header carries the brief id through to the
// realtime.call.incoming webhook.
func (m *Manager) createTwilioCall(ctx context.Context, to, briefID string) (string, error) {
	twiml := fmt.Sprintf(
		`<Response><Dial><Sip>sip:%s@sip.api.openai.com;transport=tls?X-Jarvis-Brief=%s</Sip></Dial></Response>`,
		m.cfg.ProjectID, briefID,
	)
	form := url.Values{}
	form.Set("To", to)
	form.Set("From", m.cfg.TwilioNumber)
	form.Set("Twiml", twiml)
	// Status callbacks are the ONLY signal for a call that never connects
	// (no answer, busy, failed): the <Dial><Sip> TwiML never runs and OpenAI
	// never fires our incoming webhook, so without this such a call vanishes
	// silently. Needs a public origin; degrade quietly if unset.
	if m.cfg.PublicBaseURL != "" {
		form.Set("StatusCallback", m.cfg.PublicBaseURL+"/webhooks/twilio-status?brief="+url.QueryEscape(briefID))
		form.Set("StatusCallbackEvent", "initiated ringing answered completed")
		form.Set("StatusCallbackMethod", "POST")

		// Answering-machine detection. Whether a MACHINE picked up is a FACT,
		// and facts belong in code: Jarvis was left to work it out from the
		// audio, and duly explained his whole errand to Ariana's iPhone
		// screening service instead of leaving the message.
		//
		// DetectMessageEnd + AsyncAmd: the call connects immediately (no dead
		// air while we listen), Twilio detects in parallel, and for a machine it
		// tells us once the greeting has finished, which is exactly the moment
		// to deliver. The webhook injects that into the live call.
		form.Set("MachineDetection", "DetectMessageEnd")
		form.Set("AsyncAmd", "true")
		form.Set("AsyncAmdStatusCallback", m.cfg.PublicBaseURL+"/webhooks/twilio-amd?brief="+url.QueryEscape(briefID))
		form.Set("AsyncAmdStatusCallbackMethod", "POST")
	}

	endpoint := fmt.Sprintf(twilioCallsURL, m.cfg.TwilioSID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("phone: build twilio request: %w", err)
	}
	req.SetBasicAuth(m.cfg.TwilioSID, m.cfg.TwilioToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("phone: twilio call create: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("phone: twilio call create HTTP %d: %s", resp.StatusCode, clip(string(raw), 400))
	}
	var out struct {
		SID string `json:"sid"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.SID == "" {
		return "", fmt.Errorf("phone: twilio returned no call sid: %s", clip(string(raw), 400))
	}
	return out.SID, nil
}

func str(in map[string]any, key string) string {
	if v, ok := in[key].(string); ok {
		return v
	}
	return ""
}
