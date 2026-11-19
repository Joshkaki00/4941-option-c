package retrieval

import "testing"

func ids(cs []Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ChunkID
	}
	return out
}

func TestFuseRewardsChunksPresentInBothLists(t *testing.T) {
	kw := []Candidate{{ChunkID: "a", VersionID: 1}, {ChunkID: "b", VersionID: 1}}
	vec := []Candidate{{ChunkID: "c", VersionID: 1}, {ChunkID: "b", VersionID: 1}}
	got := ids(Fuse(10, kw, vec))
	if len(got) != 3 || got[0] != "b" {
		t.Fatalf("chunk in both legs should rank first, got %v", got)
	}
}

func TestFuseRespectsLimit(t *testing.T) {
	kw := []Candidate{{ChunkID: "a", VersionID: 1}, {ChunkID: "b", VersionID: 1}, {ChunkID: "c", VersionID: 1}}
	if got := Fuse(2, kw); len(got) != 2 {
		t.Fatalf("want 2, got %d", len(got))
	}
}

func TestFuseEmptyLegs(t *testing.T) {
	if got := Fuse(5); len(got) != 0 {
		t.Fatalf("want empty, got %v", ids(got))
	}
	if got := Fuse(5, nil, nil); len(got) != 0 {
		t.Fatalf("want empty for nil legs, got %v", ids(got))
	}
}

func TestFuseKeepsSameChunkIDInDifferentVersionsSeparate(t *testing.T) {
	kw := []Candidate{{ChunkID: "a", VersionID: 1}, {ChunkID: "a", VersionID: 2}}
	if got := Fuse(10, kw); len(got) != 2 {
		t.Fatalf("different versions must not merge, got %d", len(got))
	}
}
