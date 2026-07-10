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
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

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
				"type":        "string",
				"description": "Callee number in E.164 format, e.g. +14155550123.",
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
	if !e164.MatchString(to) {
		return "", fmt.Errorf("'to' must be E.164 (e.g. +14155550123); got %q", to)
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

	// Store the brief FIRST: the incoming-call webhook rehydrates it by id,
	// so it must exist before Twilio bridges the leg.
	briefID := uuid.NewString()
	brief := &Brief{
		Topic:       strings.TrimSpace(str(in, "topic")),
		Kind:        strings.TrimSpace(str(in, "contact_kind")),
		Name:        strings.TrimSpace(str(in, "name")),
		To:          to,
		Goal:        goal,
		Constraints: constraints,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
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
