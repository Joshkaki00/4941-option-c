// Package testutil provides a Postgres test database and document seeding.
// Database tests need TEST_DATABASE_URL. Set REQUIRE_DB=1 to fail instead of skipping.
package testutil

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Joshkaki00/4941-option-c/internal/chunk"
	"github.com/Joshkaki00/4941-option-c/internal/embed"
	"github.com/Joshkaki00/4941-option-c/internal/store"
)

// advisoryLockKey serializes database tests across packages, since `go test ./...`
// runs packages in parallel against one test database.
const advisoryLockKey = 7315

// OpenDB migrates the test database, clears data, and returns a store.
func OpenDB(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("REQUIRE_DB") == "1" {
			t.Fatal("REQUIRE_DB=1 but TEST_DATABASE_URL is unset")
		}
		t.Skip("TEST_DATABASE_URL not set; skipping database test")
	}
	ctx := t.Context()

	lock, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect for lock: %v", err)
	}
	if _, err := lock.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockKey); err != nil {
		t.Fatalf("advisory lock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = lock.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", advisoryLockKey)
		_ = lock.Close(context.Background())
	})

	if err := store.Migrate(ctx, dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st, err := store.Open(ctx, dsn, 8)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(st.Close)

	if _, err := st.Pool.Exec(ctx, `TRUNCATE feedback, message_citations, messages, conversations,
		ingestion_jobs, chunks, document_versions, document_audiences, documents, audiences RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return st
}

// Doc describes a document to seed in tests.
type Doc struct {
	SourceID    string
	Title       string
	Audiences   []string
	LastUpdated time.Time
	Body        string
}

// Seed runs the same chunk → embed → publish path as ingestion.
func Seed(t *testing.T, st *store.Store, emb embed.Embedder, d Doc) {
	t.Helper()
	ctx := t.Context()
	chunks := chunk.Split(d.SourceID, d.Title, d.Body)
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Content
	}
	vecs, err := emb.Embed(ctx, texts)
	if err != nil {
		t.Fatalf("embed %s: %v", d.SourceID, err)
	}
	inputs := make([]store.ChunkInput, len(chunks))
	for i, c := range chunks {
		inputs[i] = store.ChunkInput{
			ID: c.ID, Ordinal: c.Ordinal, HeadingPath: c.HeadingPath,
			Content: c.Content, ChunkType: c.Type, Embedding: vecs[i],
		}
	}
	sum := sha256.Sum256([]byte(d.Body))
	last := d.LastUpdated
	if last.IsZero() {
		last = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if _, err := st.PublishVersion(ctx, store.DocumentVersion{
		SourceID: d.SourceID, Title: d.Title, LastUpdated: last,
		ContentHash: hex.EncodeToString(sum[:]), Audiences: d.Audiences, Chunks: inputs,
	}); err != nil {
		t.Fatalf("publish %s: %v", d.SourceID, err)
	}
}
