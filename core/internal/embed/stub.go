package embed

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"strings"
	"unicode"
)

// Stub returns a deterministic 384-dim embedding derived from token hashes.
// It's not semantically meaningful but it's stable per input, lets the rest
// of the memory subsystem compile and run end-to-end, and gives the BM25
// stream useful work to do during development. Swap to HTTP or ONNX in prod.
type Stub struct{}

func NewStub() *Stub        { return &Stub{} }
func (s *Stub) Name() string { return "stub" }
func (s *Stub) Dim() int     { return Dim }

func (s *Stub) Embed(_ context.Context, text string) ([]float32, error) {
	if strings.TrimSpace(text) == "" {
		return nil, ErrEmptyText
	}
	out := make([]float32, Dim)

	// Bag-of-words: hash each token to several dims with shifting offsets.
	for _, tok := range tokenize(text) {
		sum := sha256.Sum256([]byte(tok))
		for i := 0; i < 8; i++ {
			idx := int(binary.LittleEndian.Uint16(sum[i*2:i*2+2])) % Dim
			out[idx] += 1.0
		}
	}

	// L2 normalize
	var norm float32
	for _, v := range out {
		norm += v * v
	}
	if norm > 0 {
		n := float32(math.Sqrt(float64(norm)))
		for i := range out {
			out[i] /= n
		}
	}
	return out, nil
}

func tokenize(text string) []string {
	text = strings.ToLower(text)
	var b strings.Builder
	out := make([]string, 0, 32)
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}
