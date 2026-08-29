package db

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// "Are the migrations applied?" is the question that has cost this project the
// most: 011 through 014 sat unapplied in production for weeks while a prior
// session asserted they were live. These pin the two properties that make the
// answer trustworthy — it reads the set the BINARY carries, and it never
// answers "current" when it could not actually look.

func TestNames_ReadsTheEmbeddedSetInApplyOrder(t *testing.T) {
	names, err := Names()
	if err != nil {
		t.Fatalf("Names: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("the binary carries no migrations, so serve would boot on an unmigrated schema")
	}
	for i, n := range names {
		if !strings.HasSuffix(n, ".sql") {
			t.Fatalf("non-sql migration %q", n)
		}
		if i > 0 && names[i-1] >= n {
			t.Fatalf("out of order: %q before %q", names[i-1], n)
		}
	}
	if got := SchemaVersion(); got != names[len(names)-1] {
		t.Fatalf("SchemaVersion = %q, want the newest migration %q", got, names[len(names)-1])
	}
}

// failingQuerier stands in for a database that is there but won't answer.
type failingQuerier struct{ err error }

func (f failingQuerier) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, f.err
}

// THE load-bearing property. An empty pending list means "verified: nothing
// pending". A probe that could not run must return an ERROR, never an empty
// list, or every caller reads a failed look as a clean schema — the precise
// shape of the failure that let four migrations go missing unnoticed.
func TestPending_CannotLookIsNeverNothingPending(t *testing.T) {
	if _, err := Pending(context.Background(), nil); err == nil {
		t.Fatal("no connection must be an error, not an empty (= clean) result")
	}
	_, err := Pending(context.Background(), failingQuerier{err: errors.New(`relation "schema_migrations" does not exist`)})
	if err == nil {
		t.Fatal("an unreadable schema_migrations must be an error, not a clean result")
	}
	if !strings.Contains(err.Error(), "schema_migrations") {
		t.Fatalf("the error must carry why it could not look: %v", err)
	}
}
