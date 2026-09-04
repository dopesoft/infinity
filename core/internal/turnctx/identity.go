package turnctx

import (
	"context"
	"strings"
)

// identity.go - the two ids that let a browser match what it is looking at
// against what the server wrote down, WITHOUT comparing text.
//
// A turn used to have two ids: the one the WS layer minted for its journal and
// the one the loop minted for mem_turns. Frames carried the first, persisted
// rows the second, so the browser could not pair a live bubble with its row and
// fell back to "does this row start with the text I have?" - the last guess in
// the transcript, and the one that ate or duplicated replies. Now the server
// mints ONE id, stamps it here, and the turn store uses it for the mem_turns
// row, so a frame's turn_id and a row's turn_id are the same string.
//
// The client message id is the same idea for the boss's own messages: the
// browser mints it, it rides the `message`/`steer` frame, the loop writes it
// into the UserPromptSubmit payload, and the transcript hands it back.

type turnIDKey struct{}
type clientMessageIDKey struct{}

// WithTurnID stamps the server-minted turn id onto ctx. The turn store opens
// the mem_turns row under this id instead of minting its own.
func WithTurnID(ctx context.Context, id string) context.Context {
	id = strings.TrimSpace(id)
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, turnIDKey{}, id)
}

// TurnID returns the turn id stamped by WithTurnID, or "" when the caller
// left it to the store to mint one (crons, delegates, voice tool turns).
func TurnID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(turnIDKey{}).(string)
	return v
}

// WithClientMessageID stamps the browser's own id for the user message that
// opens this turn. Persisted on the UserPromptSubmit payload as client_id.
func WithClientMessageID(ctx context.Context, id string) context.Context {
	id = strings.TrimSpace(id)
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, clientMessageIDKey{}, id)
}

// ClientMessageID returns the id stamped by WithClientMessageID, or "".
func ClientMessageID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(clientMessageIDKey{}).(string)
	return v
}
