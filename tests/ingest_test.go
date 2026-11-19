package tests

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Joshkaki00/4941-option-c/internal/embed"
	"github.com/Joshkaki00/4941-option-c/internal/ingest"
)

const goodDoc = `---
title: PTO Policy
owner: People Operations
last_updated: 2026-03-01
audiences: all-employees, us-ca
---
# PTO Policy

## Accrual

Full-time employees accrue 15 days per year.
`

func TestParseDocumentExtractsFields(t *testing.T) {
	d, err := ingest.ParseDocument([]byte(goodDoc))
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "PTO Policy" || d.Owner != "People Operations" {
		t.Fatalf("title/owner = %q / %q", d.Title, d.Owner)
	}
	if want := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC); !d.LastUpdated.Equal(want) {
		t.Fatalf("last updated = %v", d.LastUpdated)
	}
	if len(d.Audiences) != 2 || d.Audiences[0] != "all-employees" || d.Audiences[1] != "us-ca" {
		t.Fatalf("audiences = %v", d.Audiences)
	}
	if strings.Contains(d.Body, "last_updated") {
		t.Fatal("body must not include front matter")
	}
	if !strings.HasPrefix(d.Body, "# PTO Policy") {
		t.Fatalf("body should start at the first heading, got %q", d.Body[:20])
	}
}

func TestParseDocumentAcceptsCRLF(t *testing.T) {
	raw := strings.ReplaceAll(goodDoc, "\n", "\r\n")
	if _, err := ingest.ParseDocument([]byte(raw)); err != nil {
		t.Fatalf("CRLF front matter rejected: %v", err)
	}
}

func TestParseDocumentRejectsMissingFrontMatter(t *testing.T) {
	if _, err := ingest.ParseDocument([]byte("# No front matter\n")); err == nil {
		t.Fatal("missing front matter must fail")
	}
}

func TestParseDocumentRejectsUnterminatedFrontMatter(t *testing.T) {
	if _, err := ingest.ParseDocument([]byte("---\ntitle: X\nlast_updated: 2026-01-01\n")); err == nil {
		t.Fatal("unterminated front matter must fail")
	}
}

func TestParseDocumentRequiresTitle(t *testing.T) {
	raw := "---\nlast_updated: 2026-01-01\naudiences: all-employees\n---\n# x\n"
	if _, err := ingest.ParseDocument([]byte(raw)); err == nil {
		t.Fatal("missing title must fail")
	}
}

func TestParseDocumentRejectsBadDate(t *testing.T) {
	for _, date := range []string{"", "03/01/2026", "2026-13-01"} {
		raw := "---\ntitle: X\nlast_updated: " + date + "\n---\n# x\n"
		if _, err := ingest.ParseDocument([]byte(raw)); err == nil {
			t.Fatalf("date %q must fail", date)
		}
	}
}

func TestParseDocumentWithoutAudiencesIsAllowed(t *testing.T) {
	raw := "---\ntitle: Hidden\nlast_updated: 2026-01-01\n---\n# x\n"
	d, err := ingest.ParseDocument([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Audiences) != 0 {
		t.Fatalf("audiences = %v, want none (default-deny)", d.Audiences)
	}
}

func TestParseDocumentSkipsEmptyAudienceEntries(t *testing.T) {
	raw := "---\ntitle: X\nlast_updated: 2026-01-01\naudiences: ,  ,all-employees,\n---\n# x\n"
	d, err := ingest.ParseDocument([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Audiences) != 1 || d.Audiences[0] != "all-employees" {
		t.Fatalf("audiences = %v", d.Audiences)
	}
}

func TestPrepareBuildsVersionWithHashAndEmbeddings(t *testing.T) {
	raw := []byte(goodDoc)
	v, err := ingest.Prepare(context.Background(), "pto-policy.md", raw, embed.NewHash(768))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if v.ContentHash != hex.EncodeToString(sum[:]) {
		t.Fatalf("content hash mismatch")
	}
	if v.SourceID != "pto-policy.md" || v.Title != "PTO Policy" {
		t.Fatalf("source/title = %q / %q", v.SourceID, v.Title)
	}
	if len(v.Chunks) == 0 {
		t.Fatal("no chunks")
	}
	for i, c := range v.Chunks {
		if len(c.Embedding) != 768 {
			t.Fatalf("chunk %d embedding dim = %d", i, len(c.Embedding))
		}
		if c.Ordinal != i {
			t.Fatalf("chunk %d ordinal = %d", i, c.Ordinal)
		}
		if !strings.HasPrefix(c.Content, "PTO Policy") {
			t.Fatalf("chunk %d lacks title prefix", i)
		}
	}
}

func TestPrepareRejectsDocumentWithoutContent(t *testing.T) {
	raw := []byte("---\ntitle: Empty\nlast_updated: 2026-01-01\n---\n# Empty\n")
	_, err := ingest.Prepare(context.Background(), "empty.md", raw, embed.NewHash(8))
	if err == nil || !strings.Contains(err.Error(), "no content") {
		t.Fatalf("want no-content error, got %v", err)
	}
}

func TestPrepareSurfacesEmbedderFailure(t *testing.T) {
	_, err := ingest.Prepare(context.Background(), "x.md", []byte(goodDoc), failingEmbedder{})
	if err == nil || !errors.Is(err, errBoom) {
		t.Fatalf("embedder error must be wrapped and returned, got %v", err)
	}
}

func TestPrepareRejectsVectorCountMismatch(t *testing.T) {
	_, err := ingest.Prepare(context.Background(), "x.md", []byte(goodDoc), shortEmbedder{})
	if err == nil {
		t.Fatal("fewer vectors than chunks must fail")
	}
}

var errBoom = errors.New("boom")

type failingEmbedder struct{}

func (failingEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	return nil, errBoom
}

type shortEmbedder struct{}

func (shortEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	return make([][]float32, len(texts)-1), nil
}
