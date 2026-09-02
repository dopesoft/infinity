package main

import (
	"context"
	"github.com/dopesoft/infinity/core/internal/finish"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/reauth"
)

// reauthParker implements agent.ReauthParker: it persists the parked turn to
// mem_reauth_waits AND surfaces a reconnect message into the chat session, so
// the boss learns — in the conversation — that his model needs re-auth and that
// the request wasn't lost. The reauth poller does the resume.
type reauthParker struct {
	store    *reauth.Store
	notifier reauth.Notifier
}

func (p *reauthParker) Park(ctx context.Context, sessionID, provider, model, userText, reason string) error {
	if err := p.store.Park(ctx, sessionID, provider, model, userText, reason); err != nil {
		return err
	}
	if p.notifier != nil {
		msg := "⚠️ Your model needs re-authentication, so I paused your request — I didn't lose it.\n\n" +
			llm.ReconnectHint(provider) +
			"\n\nThe moment it's back (reconnect it, or switch models in Settings) I'll finish what you asked and post the answer here automatically."
		p.notifier.Notify(sessionID, msg)
	}
	return nil
}

// reauthProber implements reauth.HealthProber by issuing a tiny probe against
// the loop's CURRENT active brain. Returns healthy unless the probe comes back
// with an auth failure — so it goes green whether the boss re-authed the failed
// model or switched the active model to a working one. Only runs while a turn
// is parked, so its token cost is negligible.
type reauthProber struct{ loop *agent.Loop }

func (h *reauthProber) Healthy(ctx context.Context) bool {
	if h.loop == nil {
		return false
	}
	prov := h.loop.Provider()
	if prov == nil {
		return false
	}
	pctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	out := make(chan llm.StreamEvent, 16)
	authFailed := false
	done := make(chan struct{})
	go func() {
		_, err := prov.Stream(pctx, "", "Reply with the single word: ok",
			[]llm.Message{{Role: llm.RoleUser, Content: "ping"}}, nil, out)
		if err != nil && llm.IsAuthFailure(err.Error()) {
			authFailed = true
		}
		close(out)
		close(done)
	}()
	for ev := range out {
		if ev.Kind == llm.StreamError && llm.IsAuthFailure(ev.Err) {
			authFailed = true
		}
	}
	<-done
	return !authFailed
}

// reauthReplayer implements reauth.Replayer over agent.Loop.Run. It replays the
// parked turn, accumulates the streamed assistant text, and returns it for the
// poller to deliver into chat. If the replay itself re-parks (the credential
// flapped back mid-replay), it returns an error so the poller keeps the wait
// open instead of delivering an empty answer.
type reauthReplayer struct{ loop *agent.Loop }

func (r *reauthReplayer) Replay(ctx context.Context, sessionID, userText, model string) (string, error) {
	out := make(chan agent.RunEvent, 64)
	var runErr error
	done := make(chan struct{})
	go func() {
		runErr = r.loop.Run(ctx, sessionID, userText, model, nil, out)
		close(out)
		close(done)
	}()

	var sb strings.Builder
	reParked := false
	for ev := range out {
		switch ev.Kind {
		case agent.EventDelta:
			sb.WriteString(ev.TextDelta)
		case agent.EventComplete:
			if ev.StopReason == "awaiting_reauth" {
				reParked = true
			}
		}
	}
	<-done
	if runErr != nil {
		return "", runErr
	}
	if reParked {
		return "", errReParked
	}
	return strings.TrimSpace(sb.String()), nil
}

// errReParked signals the replay hit another auth failure and re-parked, so the
// poller should leave the wait open rather than mark it resumed.
var errReParked = errReParkedT("re-parked during replay")

type errReParkedT string

func (e errReParkedT) Error() string { return string(e) }

// selfPromptReplayer marks every replay it forwards as a turn INFINITY
// started, so the brief behind it is filed under AgentSelfPrompt and stays out
// of the boss's transcript.
//
// It wraps at the WIRING seam rather than inside the finish poller, so the
// poller keeps its one narrow dependency (a Replayer) and the reauth path -
// which replays the boss's OWN parked words, and must stay visible - is
// untouched by construction.
type selfPromptReplayer struct{ inner finish.Replayer }

func (r selfPromptReplayer) Replay(ctx context.Context, sessionID, userText, model string) (string, error) {
	return r.inner.Replay(agent.WithSelfPrompt(ctx), sessionID, userText, model)
}
