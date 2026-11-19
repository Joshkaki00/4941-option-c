package ingest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Joshkaki00/4941-option-c/internal/embed"
	"github.com/Joshkaki00/4941-option-c/internal/store"
	"github.com/Joshkaki00/4941-option-c/internal/testutil"
)

const ptoDoc = `---
title: PTO Policy
owner: People Operations
last_updated: 2026-03-01
audiences: all-employees
---
# PTO Policy

## Allotment

Full-time employees receive 15 vacation days each year.
`

func TestParseFrontMatterRequiresTitleAndDate(t *testing.T) {
	cases := map[string]string{
		"no front matter": "# Title\n\nbody",
		"missing title":   "---\nlast_updated: 2026-01-01\n---\nbody",
		"missing date":    "---\ntitle: X\n---\nbody",
		"bad date":        "---\ntitle: X\nlast_updated: March 1st\n---\nbody",
		"unterminated":    "---\ntitle: X\nlast_updated: 2026-01-01\nbody",
	}
	for name, raw := range cases {
		if _, err := ParseDocument([]byte(raw)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestParseFrontMatterExtractsFields(t *testing.T) {
	doc, err := ParseDocument([]byte(ptoDoc))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title != "PTO Policy" || doc.Owner != "People Operations" {
		t.Fatalf("bad title/owner: %+v", doc)
	}
	if !doc.LastUpdated.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("bad date: %v", doc.LastUpdated)
	}
	if len(doc.Audiences) != 1 || doc.Audiences[0] != "all-employees" {
		t.Fatalf("bad audiences: %v", doc.Audiences)
	}
	if !strings.Contains(doc.Body, "15 vacation days") {
		t.Fatalf("body missing content: %q", doc.Body)
	}
}

func TestParseFrontMatterSplitsAudiences(t *testing.T) {
	raw := strings.Replace(ptoDoc, "audiences: all-employees", "audiences: engineering, us-ca ,managers", 1)
	doc, err := ParseDocument([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(doc.Audiences, "|") != "engineering|us-ca|managers" {
		t.Fatalf("got %v", doc.Audiences)
	}
}

func TestIngestFilePublishesAndIsIdempotent(t *testing.T) {
	st := testutil.OpenDB(t)
	emb := embed.NewHash(store.Dim)
	root := t.TempDir()
	writeFile(t, root, "pto-policy.md", ptoDoc)

	w := &Worker{Store: st, Embedder: emb, Root: root}
	if err := w.IngestFile(t.Context(), "pto-policy.md"); err != nil {
		t.Fatal(err)
	}
	// Second run with identical bytes must not create a new version.
	if err := w.IngestFile(t.Context(), "pto-policy.md"); err != nil {
		t.Fatalf("unchanged re-ingest should succeed as a no-op, got %v", err)
	}
	var versions int
	if err := st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM document_versions`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 1 {
		t.Fatalf("want 1 version after identical re-ingest, got %d", versions)
	}
}

func TestIngestChangedFileFlipsVersionAndHidesOldChunks(t *testing.T) {
	st := testutil.OpenDB(t)
	emb := embed.NewHash(store.Dim)
	root := t.TempDir()
	writeFile(t, root, "pto-policy.md", ptoDoc)
	w := &Worker{Store: st, Embedder: emb, Root: root}
	if err := w.IngestFile(t.Context(), "pto-policy.md"); err != nil {
		t.Fatal(err)
	}

	updated := strings.Replace(ptoDoc, "15 vacation days", "20 vacation days", 1)
	writeFile(t, root, "pto-policy.md", updated)
	if err := w.IngestFile(t.Context(), "pto-policy.md"); err != nil {
		t.Fatal(err)
	}

	var current, total int
	ctx := t.Context()
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM document_versions WHERE is_current`).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if current != 1 {
		t.Fatalf("exactly one current version expected, got %d", current)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM chunks WHERE is_current AND content LIKE '%20 vacation days%'`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total == 0 {
		t.Fatal("new content must be current")
	}
	var stale int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM chunks WHERE is_current AND content LIKE '%15 vacation days%'`).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Fatal("old chunks must not be current after the flip")
	}
	var retained int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM chunks WHERE content LIKE '%15 vacation days%'`).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if retained == 0 {
		t.Fatal("old chunks must be kept for audit")
	}
}

func TestDrainProcessesQueueAndMarksBadFilesFailed(t *testing.T) {
	st := testutil.OpenDB(t)
	emb := embed.NewHash(store.Dim)
	root := t.TempDir()
	writeFile(t, root, "a.md", ptoDoc)
	writeFile(t, root, "b.md", strings.Replace(ptoDoc, "PTO Policy", "Benefits", 1))
	writeFile(t, root, "bad.md", "no front matter at all")

	n, err := EnqueueDir(t.Context(), st, root)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("want 3 jobs enqueued, got %d", n)
	}

	w := &Worker{Store: st, Embedder: emb, Root: root, Concurrency: 2, MaxAttempts: 1}
	done, failed, err := w.Drain(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if done != 2 || failed != 1 {
		t.Fatalf("want done=2 failed=1, got done=%d failed=%d", done, failed)
	}

	var pending int
	if err := st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM ingestion_jobs WHERE status = 'queued'`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("no queued jobs should remain, got %d", pending)
	}
}

func TestDrainHonorsCancelledContext(t *testing.T) {
	st := testutil.OpenDB(t)
	root := t.TempDir()
	writeFile(t, root, "a.md", ptoDoc)
	if _, err := EnqueueDir(t.Context(), st, root); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	w := &Worker{Store: st, Embedder: embed.NewHash(store.Dim), Root: root}
	done, failed, _ := w.Drain(ctx)
	if done != 0 || failed != 0 {
		t.Fatalf("cancelled drain must process nothing, got done=%d failed=%d", done, failed)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
