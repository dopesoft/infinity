package mcpauth

import "testing"

// A token carries the conversation with it. If that binding is lost, every
// tool the Claude brain calls back through MCP writes itself into the void:
// stored, but attached to no conversation, so it never appears where the boss
// would look for it. Nothing errors, which is why it is asserted here.
func TestTokenCarriesItsConversation(t *testing.T) {
	m := New()
	tok := m.Mint("session-7")
	if tok == "" {
		t.Fatal("mint produced nothing")
	}
	got, ok := m.Session(tok)
	if !ok {
		t.Fatal("a freshly minted token was rejected")
	}
	if got != "session-7" {
		t.Errorf("token lost its conversation: got %q", got)
	}
}

// Two conversations must never share a token, or one brain's tool calls land
// in the other's history.
func TestTokensAreDistinctPerConversation(t *testing.T) {
	m := New()
	a, b := m.Mint("session-a"), m.Mint("session-b")
	if a == b {
		t.Fatal("two conversations were issued the same token")
	}
	if s, _ := m.Session(a); s != "session-a" {
		t.Errorf("crossed wires: %q", s)
	}
	if s, _ := m.Session(b); s != "session-b" {
		t.Errorf("crossed wires: %q", s)
	}
}

func TestUnknownTokenIsRejected(t *testing.T) {
	m := New()
	if _, ok := m.Session("not-a-real-token"); ok {
		t.Fatal("an unminted token was accepted")
	}
	if _, ok := m.Session(""); ok {
		t.Fatal("an empty bearer was accepted")
	}
}

// Revoking must actually revoke: a token that outlives its session is a live
// credential to the whole tool registry.
func TestRevokeKillsTheToken(t *testing.T) {
	m := New()
	tok := m.Mint("session-1")
	m.Revoke(tok)
	if _, ok := m.Session(tok); ok {
		t.Fatal("a revoked token still opens the registry")
	}
}
