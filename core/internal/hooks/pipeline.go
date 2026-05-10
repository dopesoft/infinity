// Package hooks implements the 12-event observation pipeline. Hooks are
// registered against event names and fired async — they never block the agent
// loop. Memory capture (PostToolUse and family) lives in capture.go.
package hooks

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Hook interface {
	Name() string
	Fire(ctx context.Context, ev Event) error
}

type Pipeline struct {
	mu     sync.RWMutex
	hooks  map[EventName][]Hook
	logger func(format string, args ...any)
}

func NewPipeline() *Pipeline {
	return &Pipeline{
		hooks:  make(map[EventName][]Hook),
		logger: func(format string, args ...any) { fmt.Printf("[hook] "+format+"\n", args...) },
	}
}

// Register attaches a hook to one or more events.
func (p *Pipeline) Register(h Hook, events ...EventName) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range events {
		p.hooks[e] = append(p.hooks[e], h)
	}
}

// RegisterFunc is a convenience for inline closures.
func (p *Pipeline) RegisterFunc(name string, fn func(ctx context.Context, ev Event) error, events ...EventName) {
	p.Register(funcHook{name: name, fn: fn}, events...)
}

// Emit fires all hooks for the event asynchronously. Errors are logged.
func (p *Pipeline) Emit(ev Event) {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	p.mu.RLock()
	hooks := append([]Hook(nil), p.hooks[ev.Name]...)
	p.mu.RUnlock()

	for _, h := range hooks {
		go func(h Hook) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if err := h.Fire(ctx, ev); err != nil {
				p.logger("%s/%s: %v", ev.Name, h.Name(), err)
			}
		}(h)
	}
}

// EmitSync is for tests / cron paths where awaiting completion matters.
func (p *Pipeline) EmitSync(ctx context.Context, ev Event) {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	p.mu.RLock()
	hooks := append([]Hook(nil), p.hooks[ev.Name]...)
	p.mu.RUnlock()

	for _, h := range hooks {
		if err := h.Fire(ctx, ev); err != nil {
			p.logger("%s/%s: %v", ev.Name, h.Name(), err)
		}
	}
}

type funcHook struct {
	name string
	fn   func(ctx context.Context, ev Event) error
}

func (f funcHook) Name() string                                  { return f.name }
func (f funcHook) Fire(ctx context.Context, ev Event) error      { return f.fn(ctx, ev) }
