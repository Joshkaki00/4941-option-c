package retrieval

import (
	"fmt"
	"sort"
)

// rrfK is the Reciprocal Rank Fusion constant (60 is the standard default).
const rrfK = 60

// Fuse merges ranked lists with Reciprocal Rank Fusion and returns the best `limit`.
// Candidates are keyed by (version, chunk), so old and new versions never merge.
func Fuse(limit int, lists ...[]Candidate) []Candidate {
	type entry struct {
		cand  Candidate
		score float64
		first int // first-seen order, for deterministic ties
	}
	byKey := map[string]*entry{}
	var order []*entry

	for _, list := range lists {
		for rank, c := range list {
			key := fmt.Sprintf("%d/%s", c.VersionID, c.ChunkID)
			e, ok := byKey[key]
			if !ok {
				e = &entry{cand: c, first: len(order)}
				byKey[key] = e
				order = append(order, e)
			}
			e.score += 1.0 / float64(rrfK+rank+1)
		}
	}

	sort.SliceStable(order, func(i, j int) bool {
		if order[i].score != order[j].score {
			return order[i].score > order[j].score
		}
		return order[i].first < order[j].first
	})

	if limit < 0 {
		limit = 0
	}
	if limit > len(order) {
		limit = len(order)
	}
	out := make([]Candidate, 0, limit)
	for _, e := range order[:limit] {
		c := e.cand
		c.Score = e.score
		out = append(out, c)
	}
	return out
}
