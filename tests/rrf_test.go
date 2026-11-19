package tests

import (
	"math"
	"testing"

	"github.com/Joshkaki00/4941-option-c/internal/retrieval"
)

func cand(version int64, chunkID string) retrieval.Candidate {
	return retrieval.Candidate{VersionID: version, ChunkID: chunkID}
}

func ids(cs []retrieval.Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ChunkID
	}
	return out
}

func TestFuseSingleListKeepsOrder(t *testing.T) {
	list := []retrieval.Candidate{cand(1, "a"), cand(1, "b"), cand(1, "c")}
	got := retrieval.Fuse(10, list)
	if want := []string{"a", "b", "c"}; !equalStrings(ids(got), want) {
		t.Fatalf("order = %v, want %v", ids(got), want)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Score > got[i-1].Score {
			t.Fatalf("scores not descending at %d", i)
		}
	}
}

func TestFuseFirstRankScoreIsReciprocal(t *testing.T) {
	got := retrieval.Fuse(1, []retrieval.Candidate{cand(1, "a")})
	want := 1.0 / 61.0
	if math.Abs(got[0].Score-want) > 1e-12 {
		t.Fatalf("score = %v, want %v (1/(60+1))", got[0].Score, want)
	}
}

func TestFuseAgreementBeatsSingleListTop(t *testing.T) {
	vector := []retrieval.Candidate{cand(1, "only-vector"), cand(1, "both")}
	keyword := []retrieval.Candidate{cand(1, "only-keyword"), cand(1, "both")}
	got := retrieval.Fuse(3, vector, keyword)
	if got[0].ChunkID != "both" {
		t.Fatalf("chunk found by both legs should rank first, got %v", ids(got))
	}
}

func TestFuseSameChunkDifferentVersionsStaySeparate(t *testing.T) {
	list := []retrieval.Candidate{cand(1, "x"), cand(2, "x")}
	got := retrieval.Fuse(10, list)
	if len(got) != 2 {
		t.Fatalf("old and new versions must not merge, got %d results", len(got))
	}
}

func TestFuseRespectsLimit(t *testing.T) {
	list := []retrieval.Candidate{cand(1, "a"), cand(1, "b"), cand(1, "c")}
	if got := retrieval.Fuse(2, list); len(got) != 2 {
		t.Fatalf("limit 2 returned %d", len(got))
	}
	if got := retrieval.Fuse(50, list); len(got) != 3 {
		t.Fatalf("limit above count returned %d, want 3", len(got))
	}
}

func TestFuseZeroLimitReturnsEmpty(t *testing.T) {
	if got := retrieval.Fuse(0, []retrieval.Candidate{cand(1, "a")}); len(got) != 0 {
		t.Fatalf("limit 0 returned %d", len(got))
	}
}

func TestFuseNoListsReturnsEmpty(t *testing.T) {
	if got := retrieval.Fuse(5); len(got) != 0 {
		t.Fatalf("no lists returned %d", len(got))
	}
	if got := retrieval.Fuse(5, nil, nil); len(got) != 0 {
		t.Fatalf("empty lists returned %d", len(got))
	}
}

func TestFuseNegativeLimitDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("negative limit panicked: %v", r)
		}
	}()
	if got := retrieval.Fuse(-1, []retrieval.Candidate{cand(1, "a")}); len(got) != 0 {
		t.Fatalf("negative limit returned %d results", len(got))
	}
}

func TestFuseTiesKeepFirstSeenOrder(t *testing.T) {
	// "a" and "b" each appear once at rank 0 in separate lists: equal scores.
	got := retrieval.Fuse(2, []retrieval.Candidate{cand(1, "a")}, []retrieval.Candidate{cand(1, "b")})
	if want := []string{"a", "b"}; !equalStrings(ids(got), want) {
		t.Fatalf("tie order = %v, want %v", ids(got), want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
