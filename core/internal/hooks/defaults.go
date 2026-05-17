package hooks

import (
	"github.com/dopesoft/infinity/core/internal/embed"
	"github.com/dopesoft/infinity/core/internal/memory"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RegisterDefaults wires the capture hook into all 12 events. Compressor may
// be nil - if so, capture still records observations but doesn't promote them.
func RegisterDefaults(p *Pipeline, pool *pgxpool.Pool, store *memory.Store, embedder embed.Embedder, compressor *memory.Compressor) {
	if pool == nil || store == nil {
		return
	}
	capture := NewCaptureHook(pool, store, embedder, CaptureOptions{Compressor: compressor})
	p.Register(capture, AllEvents...)
}
