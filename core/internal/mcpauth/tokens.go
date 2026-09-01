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
	issued map[string]time.Time
}

func New() *Tokens { return &Tokens{issued: map[string]time.Time{}} }

// Mint returns a fresh bearer token for one brain session.
func (m *Tokens) Mint() string {
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
	m.issued[tok] = time.Now().Add(TokenTTL)
	return tok
}

// Valid reports whether tok was minted here and has not expired.
func (m *Tokens) Valid(tok string) bool {
	if tok == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	exp, ok := m.issued[tok]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(m.issued, tok)
		return false
	}
	return true
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
	for tok, exp := range m.issued {
		if now.After(exp) {
			delete(m.issued, tok)
		}
	}
}

