// Package mcpauth mints and checks the bearer tokens that let a headless
// harness reach Infinity's own tool registry over MCP.
//
// It is its own package for one reason: the HTTP server CHECKS tokens and the
// Claude Max brain MINTS them, and those live in packages that must not import
// each other. Putting the store here keeps one source of truth for the
// credential instead of two half-implementations that drift.
package mcpauth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// TokenTTL bounds how long a minted session token is accepted. A brain session
// is minutes-to-hours; a day is generous and still means a token that leaks
// off the Mac dies on its own.
const TokenTTL = 24 * time.Hour

// Tokens is the mint-and-check store for bearer tokens handed to a brain
// session. In memory on purpose: a token outliving a Core restart buys
// nothing (the session it belonged to is gone too), and this keeps a live
// credential out of the database.
type Tokens struct {
	mu     sync.Mutex
	issued map[string]lease
}

// lease is one minted token: when it dies, and WHOSE conversation it belongs
// to. The session id is the load-bearing half. A tool called back through MCP
// runs with whatever context the HTTP request carries, and without the
// session stamped onto it every memory write, surface item and plan that tool
// produces lands unattributed - the orphaned-plan failure, reproduced through
// a new door. Binding the session to the token is what keeps a tool call made
// by the Claude brain indistinguishable, downstream, from one made in chat.
type lease struct {
	expires time.Time
	session string
}

func New() *Tokens { return &Tokens{issued: map[string]lease{}} }

// Mint returns a fresh bearer token bound to one conversation.
func (m *Tokens) Mint(sessionID string) string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		// crypto/rand failing is not survivable for a credential - refuse
		// rather than hand out something guessable.
		return ""
	}
	tok := hex.EncodeToString(raw)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked()
	m.issued[tok] = lease{expires: time.Now().Add(TokenTTL), session: sessionID}
	return tok
}

// Session returns the conversation a token was minted for, and whether the
// token is still good. The caller stamps that id onto the request context so
// everything the tool writes is attributed to the right conversation.
func (m *Tokens) Session(tok string) (string, bool) {
	if tok == "" {
		return "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.issued[tok]
	if !ok {
		return "", false
	}
	if time.Now().After(l.expires) {
		delete(m.issued, tok)
		return "", false
	}
	return l.session, true
}

// Revoke drops a token the moment its session ends.
func (m *Tokens) Revoke(tok string) {
	if tok == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.issued, tok)
}

func (m *Tokens) sweepLocked() {
	now := time.Now()
	for tok, l := range m.issued {
		if now.After(l.expires) {
			delete(m.issued, tok)
		}
	}
}

