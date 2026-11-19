package store_test

import (
	"testing"
	"time"

	"github.com/Joshkaki00/4941-option-c/internal/store"
	"github.com/Joshkaki00/4941-option-c/internal/testutil"
)

// Regression: intro text under the title has no heading path (nil). It must
// store as an empty array, not NULL.
func TestPublishStoresChunkWithNoHeadingPathAsEmptyArray(t *testing.T) {
	st := testutil.OpenDB(t)
	ctx := t.Context()
	vec := make([]float32, store.Dim)
	vec[0] = 1

	_, err := st.PublishVersion(ctx, store.DocumentVersion{
		SourceID:    "intro.md",
		Title:       "Intro",
		LastUpdated: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ContentHash: "h1",
		Audiences:   []string{"all-employees"},
		Chunks: []store.ChunkInput{{
			ID: "c0", Ordinal: 0, HeadingPath: nil, Content: "Intro\n\nWelcome.",
			ChunkType: "prose", Embedding: vec,
		}},
	})
	if err != nil {
		t.Fatalf("publish with nil heading path: %v", err)
	}

	var n int
	if err := st.Pool.QueryRow(ctx,
		`SELECT cardinality(heading_path) FROM chunks WHERE id = 'c0'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("want empty heading path, got cardinality %d", n)
	}
}

func TestPublishUnchangedHashIsNoOp(t *testing.T) {
	st := testutil.OpenDB(t)
	ctx := t.Context()
	vec := make([]float32, store.Dim)
	vec[0] = 1
	v := store.DocumentVersion{
		SourceID: "a.md", Title: "A", LastUpdated: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ContentHash: "same", Audiences: []string{"all-employees"},
		Chunks: []store.ChunkInput{{ID: "c1", HeadingPath: []string{"S"}, Content: "S\n\nx", ChunkType: "prose", Embedding: vec}},
	}
	if _, err := st.PublishVersion(ctx, v); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PublishVersion(ctx, v); err != store.ErrUnchanged {
		t.Fatalf("want ErrUnchanged, got %v", err)
	}
}

func TestDefaultDenyDocumentWithoutAudiencesHasNoAudienceRows(t *testing.T) {
	st := testutil.OpenDB(t)
	ctx := t.Context()
	vec := make([]float32, store.Dim)
	vec[0] = 1
	if _, err := st.PublishVersion(ctx, store.DocumentVersion{
		SourceID: "orphan.md", Title: "Orphan", LastUpdated: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ContentHash: "o", Audiences: nil,
		Chunks: []store.ChunkInput{{ID: "c1", HeadingPath: []string{}, Content: "x", ChunkType: "prose", Embedding: vec}},
	}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM document_audiences`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("no audiences given, want 0 audience rows, got %d", n)
	}
}
