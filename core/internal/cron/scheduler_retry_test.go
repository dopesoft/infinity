package cron

import (
	"context"
	"errors"
	"testing"
)

type flakyExecutor struct {
	failuresLeft int
	calls        int
}

func (e *flakyExecutor) ExecuteJob(Job) (RunSummary, error) {
	e.calls++
	if e.failuresLeft > 0 {
		e.failuresLeft--
		return RunSummary{Meta: map[string]any{"last_call": e.calls}}, errors.New("transient failure")
	}
	return RunSummary{
		Summary: "eventually worked",
		Meta:    map[string]any{"last_call": e.calls},
	}, nil
}

func TestExecuteWithRetriesRetriesTransientCronFailure(t *testing.T) {
	exec := &flakyExecutor{failuresLeft: 1}
	s := New(nil, exec)

	summary, err, attempts := s.executeWithRetries(context.Background(), Job{
		Name:           "reliable-cron",
		MaxRetries:     3,
		BackoffSeconds: 0,
	})
	if err != nil {
		t.Fatalf("expected retry to recover, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
	if exec.calls != 2 {
		t.Fatalf("expected executor to be called twice, got %d", exec.calls)
	}
	if summary.Summary != "eventually worked" {
		t.Fatalf("expected successful summary, got %q", summary.Summary)
	}
}

func TestExecuteWithRetriesStopsAfterConfiguredRetries(t *testing.T) {
	exec := &flakyExecutor{failuresLeft: 3}
	s := New(nil, exec)

	_, err, attempts := s.executeWithRetries(context.Background(), Job{
		Name:           "still-broken-cron",
		MaxRetries:     1,
		BackoffSeconds: 0,
	})
	if err == nil {
		t.Fatal("expected final error after retries are exhausted")
	}
	if attempts != 2 {
		t.Fatalf("expected initial attempt plus one retry, got %d", attempts)
	}
}

func TestRunMetaWithAttemptsOnlyAnnotatesRetries(t *testing.T) {
	base := map[string]any{"fetched": 12}
	if got := runMetaWithAttempts(base, 1); got["attempts"] != nil {
		t.Fatalf("single-attempt run should not add attempts meta: %#v", got)
	}
	got := runMetaWithAttempts(base, 3)
	if got["attempts"] != 3 {
		t.Fatalf("expected attempts=3, got %#v", got["attempts"])
	}
	if got["fetched"] != 12 {
		t.Fatalf("expected original meta preserved, got %#v", got)
	}
}
