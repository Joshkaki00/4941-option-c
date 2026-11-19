package tests

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Joshkaki00/4941-option-c/internal/chunk"
)

func TestChunkTitleIsNotRepeatedInHeadingPath(t *testing.T) {
	got := chunk.Split("pto.md", "PTO Policy", "# PTO Policy\n\n## Accrual\n\nFull-time employees accrue 15 days.")
	if len(got) != 1 {
		t.Fatalf("want 1 chunk, got %d", len(got))
	}
	if want := []string{"Accrual"}; !reflect.DeepEqual(got[0].HeadingPath, want) {
		t.Fatalf("heading path = %v, want %v", got[0].HeadingPath, want)
	}
	if !strings.HasPrefix(got[0].Content, "PTO Policy > Accrual\n\n") {
		t.Fatalf("content lacks title>heading prefix: %q", got[0].Content)
	}
}

func TestChunkIntroBeforeHeadingHasEmptyPath(t *testing.T) {
	got := chunk.Split("doc.md", "Doc", "# Doc\n\nIntro text.")
	if len(got) != 1 {
		t.Fatalf("want 1 chunk, got %d", len(got))
	}
	if len(got[0].HeadingPath) != 0 {
		t.Fatalf("intro heading path = %v, want empty", got[0].HeadingPath)
	}
	if got[0].Content != "Doc\n\nIntro text." {
		t.Fatalf("content = %q", got[0].Content)
	}
}

func TestChunkNestedHeadingsBuildFullPath(t *testing.T) {
	body := "# Doc\n\n## Leave\n\n### Sick\n\nSick leave text."
	got := chunk.Split("doc.md", "Doc", body)
	if len(got) != 1 {
		t.Fatalf("want 1 chunk, got %d", len(got))
	}
	if want := []string{"Leave", "Sick"}; !reflect.DeepEqual(got[0].HeadingPath, want) {
		t.Fatalf("heading path = %v, want %v", got[0].HeadingPath, want)
	}
}

func TestChunkSiblingHeadingResetsDeeperPath(t *testing.T) {
	// "## A" has no body of its own, so only A1 and B emit chunks.
	body := "# Doc\n\n## A\n\n### A1\n\nalpha\n\n## B\n\nbeta"
	got := chunk.Split("doc.md", "Doc", body)
	if len(got) != 2 {
		t.Fatalf("want 2 chunks, got %d", len(got))
	}
	if want := []string{"A", "A1"}; !reflect.DeepEqual(got[0].HeadingPath, want) {
		t.Fatalf("nested path = %v, want %v", got[0].HeadingPath, want)
	}
	if want := []string{"B"}; !reflect.DeepEqual(got[1].HeadingPath, want) {
		t.Fatalf("sibling path = %v, want %v", got[1].HeadingPath, want)
	}
}

func TestChunkTableStaysWhole(t *testing.T) {
	var b strings.Builder
	b.WriteString("# Doc\n\n## Bands\n\n| Level | Min | Max |\n| --- | --- | --- |\n")
	for i := 0; i < 200; i++ {
		b.WriteString("| L" + strings.Repeat("x", 5) + " | 100000 | 200000 |\n")
	}
	got := chunk.Split("bands.md", "Bands", b.String())
	tables := 0
	for _, c := range got {
		if c.Type == "table" {
			tables++
			// Data rows start with "| Lxxxxx "; the header "| Level " must not match.
			if rows := strings.Count(c.Content, "| Lxxxxx "); rows != 200 {
				t.Fatalf("table was split: %d data rows in chunk, want 200", rows)
			}
		}
	}
	if tables != 1 {
		t.Fatalf("want exactly one table chunk, got %d", tables)
	}
}

func TestChunkOversizedProseSplitsUnderBudget(t *testing.T) {
	line := strings.Repeat("word ", 20) // 100 chars
	var lines []string
	for i := 0; i < 60; i++ {
		lines = append(lines, line)
	}
	body := "# Doc\n\n## Long\n\n" + strings.Join(lines, "\n")
	got := chunk.Split("long.md", "Doc", body)
	if len(got) < 2 {
		t.Fatalf("want oversized section split into >=2 chunks, got %d", len(got))
	}
	for _, c := range got {
		if len(c.Content) > 1800 {
			t.Fatalf("chunk %d exceeds budget: %d chars", c.Ordinal, len(c.Content))
		}
	}
}

func TestChunkIDsAreStableAcrossCalls(t *testing.T) {
	body := "# Doc\n\n## A\n\nalpha\n\n## B\n\nbeta"
	first := chunk.Split("doc.md", "Doc", body)
	second := chunk.Split("doc.md", "Doc", body)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("same input must produce identical chunks and IDs")
	}
}

func TestChunkIDsChangeWithSourceID(t *testing.T) {
	body := "# Doc\n\n## A\n\nalpha"
	a := chunk.Split("one.md", "Doc", body)
	b := chunk.Split("two.md", "Doc", body)
	if a[0].ID == b[0].ID {
		t.Fatal("chunk IDs must include the source id")
	}
}

func TestChunkDuplicateHeadingsGetDistinctIDs(t *testing.T) {
	body := "# Doc\n\n## Notes\n\nfirst\n\n## Notes\n\nsecond"
	got := chunk.Split("doc.md", "Doc", body)
	if len(got) != 2 {
		t.Fatalf("want 2 chunks, got %d", len(got))
	}
	if got[0].ID == got[1].ID {
		t.Fatal("two sections with the same heading path must not share an ID")
	}
}

func TestChunkOrdinalsAreSequential(t *testing.T) {
	body := "# Doc\n\n## A\n\none\n\n## B\n\ntwo\n\n## C\n\nthree"
	got := chunk.Split("doc.md", "Doc", body)
	for i, c := range got {
		if c.Ordinal != i {
			t.Fatalf("chunk %d has ordinal %d", i, c.Ordinal)
		}
	}
}

func TestChunkEmptyBodyYieldsNothing(t *testing.T) {
	if got := chunk.Split("empty.md", "Empty", "# Empty\n\n"); len(got) != 0 {
		t.Fatalf("want no chunks, got %d", len(got))
	}
	if got := chunk.Split("empty.md", "Empty", ""); len(got) != 0 {
		t.Fatalf("want no chunks for empty body, got %d", len(got))
	}
}

func TestChunkHandlesCRLF(t *testing.T) {
	got := chunk.Split("doc.md", "Doc", "# Doc\r\n\r\n## Accrual\r\n\r\nText.")
	if len(got) != 1 {
		t.Fatalf("want 1 chunk, got %d", len(got))
	}
	if want := []string{"Accrual"}; !reflect.DeepEqual(got[0].HeadingPath, want) {
		t.Fatalf("CRLF heading path = %v, want %v", got[0].HeadingPath, want)
	}
	if strings.Contains(got[0].Content, "\r") {
		t.Fatal("content must not keep carriage returns")
	}
}

func TestChunkTypeClassification(t *testing.T) {
	cases := map[string]struct {
		body string
		want string
	}{
		"list":  {"# D\n\n## S\n\n- one\n- two", "list"},
		"prose": {"# D\n\n## S\n\nPlain sentence.", "prose"},
		"mixed": {"# D\n\n## S\n\nIntro line.\n\n- item", "prose"},
	}
	for name, tc := range cases {
		got := chunk.Split("d.md", "D", tc.body)
		if len(got) == 0 {
			t.Fatalf("%s: no chunks", name)
		}
		if got[0].Type != tc.want {
			t.Errorf("%s: type = %q, want %q", name, got[0].Type, tc.want)
		}
	}
}
