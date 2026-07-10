// webhook.go — the OpenAI Realtime SIP webhook endpoint.
//
// OpenAI signs deliveries per the Standard Webhooks spec: headers
// webhook-id / webhook-timestamp / webhook-signature, where the signature
// is base64(HMAC-SHA256(secret, "{id}.{timestamp}.{body}")) and the secret
// env is whsec_-prefixed base64. Verification is mandatory — an unsigned or
// mis-signed delivery is a 401, never processed. Without the secret the
// route 503s (fail loud; never accept unverifiable calls in prod).
package phone

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/surface"
)

// maxWebhookBody caps a delivery read; realtime.call.incoming payloads are
// tiny (call id + SIP headers).
const maxWebhookBody = 1 << 20 // 1 MiB

// signatureTolerance rejects replayed deliveries whose timestamp is too far
// from now (Standard Webhooks recommends a small window).
const signatureTolerance = 5 * time.Minute

// briefHeader is the custom SIP header the phone_call tool rides the brief
// id on, so this webhook can tell an outbound leg from a cold inbound call.
const briefHeader = "x-jarvis-brief"

// webhookEvent is the lenient shape of an OpenAI webhook delivery.
type webhookEvent struct {
	Type string `json:"type"`
	Data struct {
		CallID     string `json:"call_id"`
		SIPHeaders []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"sip_headers"`
	} `json:"data"`
}

// HandleWebhook is the http.HandlerFunc for /webhooks/openai-realtime. The
// auth middleware already exempts /webhooks/*; this handler does its own
// route-local verification (signature) instead.
func (m *Manager) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if m == nil || m.cfg.WebhookSecret == "" {
		// No secret = no way to verify authenticity. 503 loudly rather than
		// process unsigned events.
		http.Error(w, "phone webhook not configured (OPENAI_WEBHOOK_SECRET unset)", http.StatusServiceUnavailable)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody))
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}
	if err := verifyStandardWebhook(m.cfg.WebhookSecret, r.Header, body); err != nil {
		log.Printf("phone webhook: signature verification failed: %v", err)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	var ev webhookEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		log.Printf("phone webhook: unparseable payload: %v", err)
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	switch ev.Type {
	case "realtime.call.incoming":
		callID := strings.TrimSpace(ev.Data.CallID)
		if callID == "" {
			log.Printf("phone webhook: realtime.call.incoming with no call_id")
			http.Error(w, "missing call_id", http.StatusBadRequest)
			return
		}
		briefID, callerID := "", ""
		for _, h := range ev.Data.SIPHeaders {
			switch strings.ToLower(strings.TrimSpace(h.Name)) {
			case briefHeader:
				briefID = strings.TrimSpace(h.Value)
			case "from":
				callerID = strings.TrimSpace(h.Value)
			}
		}
		// Accept + monitor run async so OpenAI gets its 200 fast; every
		// failure inside is loud (stderr + surfaced), never a silent drop.
		go m.handleIncoming(callID, briefID, callerID)
		writeOK(w)
	default:
		// Unknown event types are acknowledged and ignored — OpenAI adds
		// types over time and a 4xx would make it retry forever.
		infoLog.Printf("phone webhook: ignoring event type %q", ev.Type)
		writeOK(w)
	}
}

// handleIncoming runs off the webhook goroutine: resolve the persona (+
// brief for outbound legs), accept the call, then monitor it to the end.
func (m *Manager) handleIncoming(callID, briefID, callerID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	direction := "inbound"
	var brief *Brief
	if briefID != "" {
		direction = "outbound"
		b, err := m.loadBrief(ctx, briefID)
		if err != nil {
			// The leg we ourselves placed has lost its brief — running the
			// call unbriefed would violate the whole producer/field-agent
			// contract. Reject and surface.
			m.failCall(ctx, callID, direction, fmt.Errorf("brief %s could not be loaded: %w", briefID, err))
			return
		}
		brief = b
	}

	persona, err := m.loadPersona(ctx, direction)
	if err != nil {
		m.failCall(ctx, callID, direction, err)
		return
	}

	instructions := buildInstructions(persona, brief, callerID)
	if err := m.acceptCall(ctx, callID, instructions); err != nil {
		m.failCall(ctx, callID, direction, err)
		return
	}
	infoLog.Printf("phone: accepted %s call %s (model=%s voice=%s)", direction, callID, m.cfg.Model, m.cfg.Voice)

	// Monitor blocks until the call ends; it owns transcript capture and
	// outcome delivery (surface + push) and books the mem_runs row.
	go m.Monitor(callID, direction, brief)
}

// failCall is the loud path for a call we could not run: reject the SIP leg
// (fast busy beats an unbriefed model answering), log to stderr, and put a
// card on the calls surface so the boss sees the missed call — an inbound
// call that silently vanished would be the classic empty-because-broken lie.
func (m *Manager) failCall(ctx context.Context, callID, direction string, cause error) {
	log.Printf("phone: %s call %s failed before connect: %v", direction, callID, cause)
	if err := m.rejectCall(ctx, callID); err != nil {
		log.Printf("phone: reject call %s also failed: %v", callID, err)
	}
	if m.surface != nil {
		importance := 80
		if _, err := m.surface.Upsert(ctx, &surface.Item{
			Surface:    "calls",
			Kind:       "call",
			Source:     "phone",
			ExternalID: callID,
			Title:      "Missed " + direction + " call — could not connect",
			Body:       "I had to decline this call: " + cause.Error(),
			Importance: &importance,
		}); err != nil {
			log.Printf("phone: surface missed-call %s: %v", callID, err)
		}
	}
}

// verifyStandardWebhook checks the Standard Webhooks signature headers
// against the raw body. Constant-time compare; any missing piece fails.
func verifyStandardWebhook(secret string, hdr http.Header, body []byte) error {
	id := strings.TrimSpace(hdr.Get("webhook-id"))
	ts := strings.TrimSpace(hdr.Get("webhook-timestamp"))
	sig := strings.TrimSpace(hdr.Get("webhook-signature"))
	if id == "" || ts == "" || sig == "" {
		return fmt.Errorf("missing webhook-id/timestamp/signature headers")
	}

	// Replay window.
	epoch, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("bad webhook-timestamp %q", ts)
	}
	if d := time.Since(time.Unix(epoch, 0)); d > signatureTolerance || d < -signatureTolerance {
		return fmt.Errorf("webhook-timestamp outside tolerance (%s)", d.Round(time.Second))
	}

	// Secret is whsec_ + base64(key).
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil {
		return fmt.Errorf("OPENAI_WEBHOOK_SECRET is not whsec_-prefixed base64: %w", err)
	}

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + ts + "."))
	mac.Write(body)
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	// The header may carry several space-separated "v1,<sig>" entries
	// (key rotation); any match passes.
	for _, part := range strings.Fields(sig) {
		cand := strings.TrimPrefix(part, "v1,")
		if hmac.Equal([]byte(cand), []byte(want)) {
			return nil
		}
	}
	return fmt.Errorf("no signature matched")
}

func writeOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
