package chunk

import (
	"strings"
	"testing"
)

func TestSplitEmptyBodyYieldsNoChunks(t *testing.T) {
	if got := Split("pto.md", "PTO Policy", "   \n\n  "); len(got) != 0 {
		t.Fatalf("want 0 chunks, got %d", len(got))
	}
}

func TestSplitPrefixesTitleAndHeadingPath(t *testing.T) {
	body := "# PTO Policy\n\nIntro text.\n\n## Accrual\n\nFull-time employees accrue 15 days.\n"
	chunks := Split("pto.md", "PTO Policy", body)

	var accrual *Chunk
	for i := range chunks {
		if len(chunks[i].HeadingPath) == 1 && chunks[i].HeadingPath[0] == "Accrual" {
			accrual = &chunks[i]
		}
	}
	if accrual == nil {
		t.Fatalf("no chunk with heading path [Accrual]; got %+v", chunks)
	}
	if !strings.HasPrefix(accrual.Content, "PTO Policy > Accrual\n\n") {
		t.Fatalf("content missing title/heading prefix: %q", accrual.Content)
	}
	if !strings.Contains(accrual.Content, "accrue 15 days") {
		t.Fatalf("content missing body: %q", accrual.Content)
	}
}

func TestSplitKeepsTablesWhole(t *testing.T) {
	table := "| Years | Days |\n|---|---|\n| 0-2 | 15 |\n| 3+ | 20 |"
	body := "# Doc\n\n## Table\n\n" + table + "\n"
	chunks := Split("doc.md", "Doc", body)
	if len(chunks) != 1 {
		t.Fatalf("want 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Type != "table" {
		t.Fatalf("want type table, got %q", chunks[0].Type)
	}
	if !strings.Contains(chunks[0].Content, table) {
		t.Fatalf("table was split or altered: %q", chunks[0].Content)
	}
}

func TestSplitOversizedTableIsNotCutMidRow(t *testing.T) {
	var rows []string
	rows = append(rows, "| A | B |", "|---|---|")
	for i := 0; i < 200; i++ {
		rows = append(rows, "| row | value that makes this table long enough to exceed the chunk budget |")
	}
	body := "## Big\n\n" + strings.Join(rows, "\n") + "\n"
	chunks := Split("big.md", "Big", body)
	if len(chunks) != 1 {
		t.Fatalf("oversized table must stay in one chunk, got %d", len(chunks))
	}
}

func TestSplitLargeProseIsSplitIntoBoundedChunks(t *testing.T) {
	para := strings.Repeat("word ", 100) + "."
	var paras []string
	for i := 0; i < 20; i++ {
		paras = append(paras, para)
	}
	body := "## Long\n\n" + strings.Join(paras, "\n\n") + "\n"
	chunks := Split("long.md", "Long", body)
	if len(chunks) < 2 {
		t.Fatalf("want multiple chunks for large prose, got %d", len(chunks))
	}
	for _, c := range chunks {
		if len(c.Content) > maxChars+len("Long > Long\n\n")+200 {
			t.Fatalf("chunk exceeds budget: %d chars", len(c.Content))
		}
		if c.Type != "prose" {
			t.Fatalf("want prose, got %q", c.Type)
		}
	}
}

func TestSplitIDsAreStableAcrossRuns(t *testing.T) {
	body := "## A\n\nOne.\n\n## B\n\nTwo.\n"
	first := Split("doc.md", "Doc", body)
	second := Split("doc.md", "Doc", body)
	if len(first) != len(second) {
		t.Fatalf("chunk count changed between runs")
	}
	for i := range first {
		if first[i].ID != second[i].ID {
			t.Fatalf("id changed between runs: %s vs %s", first[i].ID, second[i].ID)
		}
	}
}

func TestSplitIDDependsOnSourceAndHeadingPath(t *testing.T) {
	body := "## A\n\nSame text.\n"
	a := Split("one.md", "Doc", body)
	b := Split("two.md", "Doc", body)
	if a[0].ID == b[0].ID {
		t.Fatalf("same heading in different documents must not share an id")
	}
}

func TestSplitListChunkType(t *testing.T) {
	body := "## Steps\n\n- first\n- second\n- third\n"
	chunks := Split("steps.md", "Steps", body)
	if len(chunks) != 1 || chunks[0].Type != "list" {
		t.Fatalf("want one list chunk, got %+v", chunks)
	}
}
