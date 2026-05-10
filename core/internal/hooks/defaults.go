package hooks

import (
	"github.com/dopesoft/infinity/core/internal/embed"
	"github.com/dopesoft/infinity/core/internal/memory"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RegisterDefaults wires the capture hook into all 12 events. Each event
// type gets the same capture so no observation slips by — the importance and
// payload shape varies, but the pipeline is uniform.
func RegisterDefaults(p *Pipeline, pool *pgxpool.Pool, store *memory.Store, embedder embed.Embedder) {
	if pool == nil || store == nil {
		return
	}
	capture := NewCaptureHook(pool, store, embedder)
	p.Register(capture, AllEvents...)
}
