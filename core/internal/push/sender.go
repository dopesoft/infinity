package push

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// Notification is the payload encrypted + delivered to one or more
// devices. Mirrors what the service worker expects (see studio/public/sw.js).
//
// Required: Title.
// Recommended: Body, Tag (dedupe), URL (deep-link on tap), Kind/ID
// (encoded into `data` for the SW to read).
//
// The service worker reads `data.url` to deep-link on tap, so always
// set URL to the most specific Studio surface for the event (e.g.
// `/trust/<id>`).
type Notification struct {
	Title              string `json:"title"`
	Body               string `json:"body,omitempty"`
	Tag                string `json:"tag,omitempty"`
	URL                string `json:"url,omitempty"`
	Icon               string `json:"icon,omitempty"`
	Badge              string `json:"badge,omitempty"`
	Kind               string `json:"kind,omitempty"`
	ID                 string `json:"id,omitempty"`
	Renotify           bool   `json:"renotify,omitempty"`
	RequireInteraction bool   `json:"requireInteraction,omitempty"`
	Silent             bool   `json:"silent,omitempty"`
}

// Sender encrypts + delivers notifications via the user's browser push
// service. VAPID keys + subject identify Core to FCM/APNs as the legit
// origin of the push.
//
// Construct via NewSenderFromEnv (production path) or NewSender (tests).
// A nil Sender is safe — Notify becomes a no-op, which is how Core
// behaves when VAPID is unconfigured.
type Sender struct {
	store       *Store
	publicKey   string
	privateKey  string
	subject     string
	ttlSeconds  int
	httpClient  *http.Client
	logger      *slog.Logger
}

// SenderConfig captures every knob — most users want NewSenderFromEnv.
type SenderConfig struct {
	Store      *Store
	PublicKey  string
	PrivateKey string
	Subject    string        // mailto:... or https://...
	TTL        time.Duration // delivery TTL the push service caches the message for
	Logger     *slog.Logger
}

func NewSender(cfg SenderConfig) *Sender {
	ttl := int(cfg.TTL.Seconds())
	if ttl <= 0 {
		ttl = 60 * 60 * 24 // 24h default
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Sender{
		store:      cfg.Store,
		publicKey:  cfg.PublicKey,
		privateKey: cfg.PrivateKey,
		subject:    cfg.Subject,
		ttlSeconds: ttl,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		logger:     logger,
	}
}

// NewSenderFromEnv reads VAPID_PUBLIC_KEY, VAPID_PRIVATE_KEY, and
// VAPID_SUBJECT from env. Returns a nil Sender + error when any of the
// three are missing so the caller can decide whether to fail the boot
// (require push) or no-op (push disabled).
func NewSenderFromEnv(store *Store, logger *slog.Logger) (*Sender, error) {
	pub := os.Getenv("VAPID_PUBLIC_KEY")
	priv := os.Getenv("VAPID_PRIVATE_KEY")
	subj := os.Getenv("VAPID_SUBJECT")
	if pub == "" || priv == "" || subj == "" {
		return nil, errors.New("VAPID_PUBLIC_KEY, VAPID_PRIVATE_KEY, VAPID_SUBJECT all required")
	}
	if !strings.HasPrefix(subj, "mailto:") && !strings.HasPrefix(subj, "https://") {
		// Both forms are valid per RFC 8292; warn but don't fail.
		logger.Warn("push: VAPID_SUBJECT should start with mailto: or https://", "got", subj)
	}
	return NewSender(SenderConfig{
		Store:      store,
		PublicKey:  pub,
		PrivateKey: priv,
		Subject:    subj,
		Logger:     logger,
	}), nil
}

// Configured returns true when this sender has VAPID material wired up.
// Used by handlers to short-circuit with 503 when push is disabled.
func (s *Sender) Configured() bool {
	return s != nil && s.publicKey != "" && s.privateKey != "" && s.subject != ""
}

// PublicKey is exposed so /api/push/vapid can hand it to the browser.
func (s *Sender) PublicKey() string {
	if s == nil {
		return ""
	}
	return s.publicKey
}

// DeliveryResult records the outcome of one fan-out attempt. Returned
// from Notify so callers (e.g. the test endpoint) can show a per-device
// summary. Errors don't propagate up because one dead device shouldn't
// fail the rest — instead the caller can surface the list.
type DeliveryResult struct {
	Endpoint   string `json:"endpoint"`
	Label      string `json:"label,omitempty"`
	StatusCode int    `json:"status,omitempty"`
	Err        string `json:"error,omitempty"`
}

// Notify delivers `n` to every active subscription. Returns one result
// per device.
func (s *Sender) Notify(ctx context.Context, n Notification) []DeliveryResult {
	if !s.Configured() || s.store == nil {
		return nil
	}
	subs, err := s.store.Active(ctx)
	if err != nil {
		s.logger.Error("push: list active subscriptions", "err", err)
		return nil
	}
	if len(subs) == 0 {
		return nil
	}
	if n.Icon == "" {
		n.Icon = "/icon.svg"
	}
	if n.Badge == "" {
		n.Badge = "/icon.svg"
	}
	payload, err := json.Marshal(n)
	if err != nil {
		s.logger.Error("push: marshal notification", "err", err)
		return nil
	}

	results := make([]DeliveryResult, 0, len(subs))
	for _, sub := range subs {
		r := s.deliverOne(ctx, sub, payload)
		results = append(results, r)
	}
	return results
}

// NotifyOne delivers a notification to a single endpoint. Used when the
// boss explicitly targets one device (test endpoint, "send only to my
// phone" style flows).
func (s *Sender) NotifyOne(ctx context.Context, endpoint string, n Notification) (DeliveryResult, error) {
	if !s.Configured() || s.store == nil {
		return DeliveryResult{}, errors.New("push: not configured")
	}
	subs, err := s.store.All(ctx)
	if err != nil {
		return DeliveryResult{}, err
	}
	for _, sub := range subs {
		if sub.Endpoint == endpoint {
			if sub.Revoked {
				return DeliveryResult{Endpoint: endpoint, Err: "revoked"}, nil
			}
			if n.Icon == "" {
				n.Icon = "/icon.svg"
			}
			payload, err := json.Marshal(n)
			if err != nil {
				return DeliveryResult{}, err
			}
			return s.deliverOne(ctx, sub, payload), nil
		}
	}
	return DeliveryResult{}, fmt.Errorf("push: endpoint not found")
}

func (s *Sender) deliverOne(ctx context.Context, sub Subscription, payload []byte) DeliveryResult {
	res := DeliveryResult{Endpoint: sub.Endpoint, Label: sub.Label}
	wpSub := &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys: webpush.Keys{
			P256dh: sub.P256dh,
			Auth:   sub.AuthKey,
		},
	}
	opts := &webpush.Options{
		HTTPClient:      s.httpClient,
		Subscriber:      s.subject,
		VAPIDPublicKey:  s.publicKey,
		VAPIDPrivateKey: s.privateKey,
		TTL:             s.ttlSeconds,
		Urgency:         webpush.UrgencyNormal,
	}
	resp, err := webpush.SendNotificationWithContext(ctx, payload, wpSub, opts)
	if err != nil {
		res.Err = err.Error()
		s.logger.Warn("push: send", "endpoint", truncEndpoint(sub.Endpoint), "err", err)
		return res
	}
	defer resp.Body.Close()
	// Drain body so the connection can be reused.
	_, _ = io.Copy(io.Discard, resp.Body)
	res.StatusCode = resp.StatusCode
	switch {
	case resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusNotFound:
		// 410/404 — subscription is dead. Delete the row so we stop
		// trying. Boss can re-subscribe from the device.
		_ = s.store.Delete(ctx, sub.Endpoint)
		res.Err = "subscription gone — removed"
	case resp.StatusCode >= 400:
		// Other errors — mark revoked so Settings can show the failure
		// without us aggressively re-trying every event.
		_ = s.store.MarkRevoked(ctx, sub.Endpoint)
		res.Err = fmt.Sprintf("push service returned %d", resp.StatusCode)
	default:
		_ = s.store.Touch(ctx, sub.Endpoint)
	}
	return res
}

// truncEndpoint shortens a push endpoint URL for log lines. Endpoints
// are ~100+ chars long; we don't want them dominating every log entry.
func truncEndpoint(ep string) string {
	if len(ep) <= 48 {
		return ep
	}
	return ep[:24] + "…" + ep[len(ep)-12:]
}

// GenerateVAPIDKeys returns a fresh VAPID keypair as URL-safe base64
// strings. Used by the `infinity vapid` CLI subcommand.
func GenerateVAPIDKeys() (private, public string, err error) {
	return webpush.GenerateVAPIDKeys()
}
