package proactive

import (
	"context"
	"fmt"
	"time"
)

// ClaudeAuthChecklist warns BEFORE the Claude subscription stops answering,
// instead of letting the boss find out from a failed turn at 3am.
//
// Two credentials carry this plan and both expire. The Mac's sign-in is
// renewed by using it, so it rarely lapses. The cloud box's token is minted by
// `claude setup-token` and lasts a YEAR, which is exactly long enough for
// everyone to forget it exists - and it dies on a date nobody wrote down,
// most likely mid-way through a nightly build with the laptop shut.
//
// So this is a calendar problem, not an error-handling problem. Waiting for
// the failure is the anti-pattern: by then the run has already been lost.
//
// Three conditions, three stable tags, so a repeat tick supersedes the last
// finding rather than piling another copy on the board.
func ClaudeAuthChecklist(read func(context.Context) (ClaudeAuthState, error)) Checklist {
	return func(ctx context.Context, h *Heartbeat) ([]Finding, error) {
		if read == nil {
			return nil, nil
		}
		st, err := read(ctx)
		if err != nil {
			return nil, err
		}
		if !st.Configured {
			// The brain isn't wired on this deploy at all. Nothing to warn
			// about, and a nag about an unused feature is noise.
			return nil, nil
		}

		switch {
		case !st.MacReady && !st.CloudReady:
			return []Finding{{
				Kind:      "outcome",
				SourceTag: "claude_auth:none",
				Title:     "I can't reach your Claude plan",
				Detail: "Neither your Mac nor the cloud machine can sign in to Claude right now, so anything " +
					"meant to run on your subscription (coding especially) has nowhere to go. Waking the Mac fixes " +
					"it, or run `claude setup-token` on the Mac and paste the token into Settings so the cloud " +
					"machine can carry it on its own.",
			}}, nil

		case st.CloudReady && st.TokenExpired():
			return []Finding{{
				Kind:      "outcome",
				SourceTag: "claude_auth:expired",
				Title:     "Your Claude token for the cloud machine has run out",
				Detail: fmt.Sprintf(
					"The token you saved %s ago has passed the one-year mark Anthropic issues them for, so the cloud "+
						"machine can no longer sign in as you. Run `claude setup-token` on your Mac and paste the new "+
						"one into Settings. Until then this only works while the Mac is awake.",
					humanMonths(st.TokenAge)),
			}}, nil

		case st.CloudReady && st.TokenExpiringSoon():
			return []Finding{{
				Kind:      "outcome",
				SourceTag: "claude_auth:expiring",
				Title:     "Your Claude token expires within the month",
				Detail: fmt.Sprintf(
					"The token the cloud machine signs in with is %s old, and they last a year. Worth replacing now "+
						"rather than finding out during an overnight build: run `claude setup-token` on your Mac and "+
						"paste the new one into Settings.",
					humanMonths(st.TokenAge)),
			}}, nil

		case st.MacReady && !st.CloudReady:
			// Everything works today, but only while the laptop is open. Said
			// once (the tag keeps it from repeating) because the failure it
			// predicts lands overnight, when he is not there to fix it.
			return []Finding{{
				Kind:      "outcome",
				SourceTag: "claude_auth:cloud_missing",
				Title:     "Claude only works here while your Mac is awake",
				Detail: "The cloud machine has no Claude token saved, so anything running overnight on your " +
					"subscription stops the moment you shut the laptop. Run `claude setup-token` on your Mac and " +
					"paste it into Settings to close that gap.",
			}}, nil
		}
		return nil, nil
	}
}

// ClaudeAuthState is the checklist's read of both credentials.
type ClaudeAuthState struct {
	// Configured is false when this brain isn't wired on this deploy. Keeps
	// the checklist silent rather than nagging about an unused feature.
	Configured bool
	MacReady   bool
	CloudReady bool
	// TokenAge is how long ago the cloud token was saved. Zero when none is
	// stored, or when it was saved before Infinity started recording the
	// date - in which case no age-based warning fires, because guessing an
	// expiry and being wrong is worse than staying quiet.
	TokenAge time.Duration
}

// claudeTokenLife is the year Anthropic issues a setup-token for.
const claudeTokenLife = 365 * 24 * time.Hour

// claudeTokenWarnAt is when to start saying something: a month out, which is
// enough notice to act on without it becoming background noise.
const claudeTokenWarnAt = claudeTokenLife - 30*24*time.Hour

func (s ClaudeAuthState) TokenExpired() bool {
	return s.TokenAge > 0 && s.TokenAge >= claudeTokenLife
}

func (s ClaudeAuthState) TokenExpiringSoon() bool {
	return s.TokenAge > 0 && s.TokenAge >= claudeTokenWarnAt && s.TokenAge < claudeTokenLife
}

// humanMonths renders an age the way he would say it out loud.
func humanMonths(d time.Duration) string {
	days := int(d.Hours() / 24)
	switch {
	case days < 45:
		return fmt.Sprintf("%d days", days)
	case days < 365:
		return fmt.Sprintf("about %d months", (days+15)/30)
	default:
		return "over a year"
	}
}
