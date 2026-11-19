package retrieval

import (
	"strings"
	"testing"

	"github.com/Joshkaki00/4941-option-c/internal/embed"
	"github.com/Joshkaki00/4941-option-c/internal/store"
	"github.com/Joshkaki00/4941-option-c/internal/testutil"
)

// Privacy gate (spec.md Gate 3). Seed:
//   - D_public: audience all-employees
//   - D_secret: audience hr-only only
//   - D_orphan: no document_audiences rows (must be invisible)
const (
	secretMarker = "compensation band L5 salary range"
	orphanMarker = "orphan document with no audience rows"
)

func seedPrivacyFixture(t *testing.T, st *store.Store, emb embed.Embedder) {
	t.Helper()
	testutil.Seed(t, st, emb, testutil.Doc{
		SourceID:  "pto-policy.md",
		Title:     "PTO Policy",
		Audiences: []string{"all-employees"},
		Body:      "# PTO Policy\n\n## Allotment\n\nFull-time employees receive vacation days each year.\n",
	})
	testutil.Seed(t, st, emb, testutil.Doc{
		SourceID:  "compensation-bands.md",
		Title:     "Compensation Bands",
		Audiences: []string{"hr-only"},
		Body:      "# Compensation Bands\n\n## Levels\n\nThe " + secretMarker + " is confidential vacation budget data.\n",
	})
	testutil.Seed(t, st, emb, testutil.Doc{
		SourceID:  "orphan.md",
		Title:     "Orphan",
		Audiences: nil,
		Body:      "# Orphan\n\n## Notes\n\nThe " + orphanMarker + " mentions vacation.\n",
	})
}

func TestAudienceFilterExcludesRestrictedAndOrphanChunks(t *testing.T) {
	st := testutil.OpenDB(t)
	emb := embed.NewHash(store.Dim)
	seedPrivacyFixture(t, st, emb)
	s := New(st, emb)
	ctx := t.Context()

	query := "vacation days compensation band salary range orphan document"
	res, err := s.Hybrid(ctx, []string{"all-employees"}, query, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) == 0 {
		t.Fatal("expected at least the public PTO chunk")
	}
	for _, c := range res.Candidates {
		if c.SourceID != "pto-policy.md" {
			t.Fatalf("employee saw chunk from %s: %q", c.SourceID, c.Content)
		}
		if strings.Contains(c.Content, secretMarker) || strings.Contains(c.Content, orphanMarker) {
			t.Fatalf("restricted text leaked into results: %q", c.Content)
		}
	}
}

func TestAudienceFilterAllowsMatchingAudience(t *testing.T) {
	st := testutil.OpenDB(t)
	emb := embed.NewHash(store.Dim)
	seedPrivacyFixture(t, st, emb)
	s := New(st, emb)

	res, err := s.Hybrid(t.Context(), []string{"hr-only"}, "compensation band salary range", 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range res.Candidates {
		if c.SourceID == "compensation-bands.md" {
			found = true
		}
	}
	if !found {
		t.Fatal("hr-only user should see the hr-only document")
	}
}

func TestAudienceFilterEmptyAudiencesDenyAll(t *testing.T) {
	st := testutil.OpenDB(t)
	emb := embed.NewHash(store.Dim)
	seedPrivacyFixture(t, st, emb)
	s := New(st, emb)

	res, err := s.Hybrid(t.Context(), nil, "vacation days", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) != 0 {
		t.Fatalf("no audiences must return nothing, got %d", len(res.Candidates))
	}
}

func TestAudienceFilterChunkPreviewReturns403ForRestricted(t *testing.T) {
	st := testutil.OpenDB(t)
	emb := embed.NewHash(store.Dim)
	seedPrivacyFixture(t, st, emb)
	s := New(st, emb)
	ctx := t.Context()

	res, err := s.Hybrid(ctx, []string{"hr-only"}, "compensation band salary range", 10)
	if err != nil || len(res.Candidates) == 0 {
		t.Fatalf("setup: need an hr-only chunk id, err=%v", err)
	}
	secretID := res.Candidates[0].ChunkID

	view, found, err := s.ChunkForUser(ctx, []string{"all-employees"}, secretID)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("chunk exists; found must be true so the handler can return 403")
	}
	if view.Allowed {
		t.Fatal("employee must not be allowed to preview hr-only chunk")
	}
	if view.Content != "" {
		t.Fatalf("denied preview must not carry content, got %q", view.Content)
	}
}

func TestAudienceFilterChunkPreviewUnknownIDNotFound(t *testing.T) {
	st := testutil.OpenDB(t)
	emb := embed.NewHash(store.Dim)
	seedPrivacyFixture(t, st, emb)
	s := New(st, emb)

	_, found, err := s.ChunkForUser(t.Context(), []string{"all-employees"}, "does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("unknown chunk id must not be found")
	}
}
