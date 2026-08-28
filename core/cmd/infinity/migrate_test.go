package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// The embedded set is what `serve` migrates from at boot, so an empty or
// unsorted set would silently apply nothing (or apply out of order).
func TestLoadMigrationsEmbeddedIsOrdered(t *testing.T) {
	files, err := loadMigrations("")
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("embedded migration set is empty; serve would boot on an unmigrated schema")
	}
	for i, m := range files {
		if !strings.HasSuffix(m.name, ".sql") {
			t.Errorf("non-sql migration %q", m.name)
		}
		if i > 0 && files[i-1].name >= m.name {
			t.Errorf("out of order: %q before %q", files[i-1].name, m.name)
		}
	}
}

// Boot must abort rather than serve on a schema it could not verify. An
// unreachable database is the cheapest way to prove the error propagates
// instead of being swallowed into a degraded-but-listening start.
func TestApplyMigrationsFailsWhenDatabaseUnreachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Port 1 refuses immediately; no external dependency, no hang.
	n, err := applyMigrations(ctx, "postgres://infinity:pw@127.0.0.1:1/infinity", "", nil)
	if err == nil {
		t.Fatal("want error for unreachable database, got nil")
	}
	if n != 0 {
		t.Errorf("applied = %d, want 0", n)
	}
}

// A missing or empty migration directory means we cannot prove the schema is
// current, so it is an error rather than a silent no-op success.
func TestApplyMigrationsFailsOnEmptySet(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Deliberately unreachable DSN: the empty set must be caught before dialing,
	// so this asserts fail-fast ordering as well as the error itself.
	_, err := applyMigrations(ctx, "postgres://infinity:pw@127.0.0.1:1/infinity", dir, nil)
	if err == nil {
		t.Fatal("want error for empty migration set, got nil")
	}
	if !strings.Contains(err.Error(), "no migrations found") {
		t.Fatalf("want 'no migrations found' before connecting, got %v", err)
	}
}

func TestApplyMigrationsReportsStatusPerMigration(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/001_a.sql", []byte("SELECT 1;"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	called := false
	_, err := applyMigrations(ctx, "postgres://infinity:pw@127.0.0.1:1/infinity", dir, func(string, bool) {
		called = true
	})
	if err == nil {
		t.Fatal("want connect error, got nil")
	}
	if called {
		t.Error("onStatus fired without applying anything")
	}
}

// The ordering invariant this pass exists to guarantee: serve applies migrations
// before it starts the HTTP server, and treats a migration failure as fatal to
// boot. Checked against the source because booting serve in a unit test would
// need a live database, an LLM provider, and MCP connectivity. If someone later
// moves the migration pass after the listener, backgrounds it, or downgrades the
// failure to a warning, this fails.
func TestServeMigratesBeforeServingAndFailsHard(t *testing.T) {
	src, err := os.ReadFile("serve.go")
	if err != nil {
		t.Fatalf("read serve.go: %v", err)
	}
	body := string(src)

	migrateAt := strings.Index(body, "applyMigrations(mctx")
	if migrateAt < 0 {
		t.Fatal("serve no longer applies migrations at boot")
	}
	startAt := strings.Index(body, "srv.Start()")
	if startAt < 0 {
		t.Fatal("could not locate srv.Start() in serve.go")
	}
	if migrateAt > startAt {
		t.Error("serve starts the HTTP server before applying migrations")
	}

	// The failure must abort boot. A warning here would let the process bind the
	// port and answer /health on an unmigrated schema.
	tail := body[migrateAt:]
	if end := strings.Index(tail, "pgxpool.ParseConfig"); end > 0 {
		tail = tail[:end]
	}
	if !strings.Contains(tail, `return fmt.Errorf("migrate: %w", mErr)`) {
		t.Error("migration failure at boot is not returned as a fatal error")
	}
}
