package memory

import "sort"

// ScoredItem is a single ranked result from one retrieval stream.
type ScoredItem struct {
	ID    string
	Rank  int     // 1-based rank within its stream
	Score float64 // optional original score; ignored by RRF
}

// RRF performs Reciprocal Rank Fusion over multiple streams.
// score(d) = Σ 1 / (k + rank_in_stream(d)) summed across all streams that surface d.
// Default k = 60 (matches the agentmemory port spec).
func RRF(streams [][]ScoredItem, k float64) []ScoredItem {
	if k <= 0 {
		k = 60
	}
	scores := make(map[string]float64)
	for _, stream := range streams {
		for _, item := range stream {
			scores[item.ID] += 1.0 / (k + float64(item.Rank))
		}
	}
	out := make([]ScoredItem, 0, len(scores))
	for id, sc := range scores {
		out = append(out, ScoredItem{ID: id, Score: sc})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	for i := range out {
		out[i].Rank = i + 1
	}
	return out
}

// DiversifyBySession walks the result list and skips any result whose
// session_id has already produced `maxPerSession` hits. Preserves order.
func DiversifyBySession(results []SearchResult, maxPerSession int) []SearchResult {
	if maxPerSession <= 0 {
		return results
	}
	count := make(map[string]int)
	out := make([]SearchResult, 0, len(results))
	for _, r := range results {
		if count[r.SessionID] >= maxPerSession {
			continue
		}
		count[r.SessionID]++
		out = append(out, r)
	}
	return out
}
