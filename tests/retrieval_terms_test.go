package tests

import (
	"reflect"
	"testing"

	"github.com/Joshkaki00/4941-option-c/internal/retrieval"
)

func TestQueryTermsLowercasesAndDropsStopwords(t *testing.T) {
	got := retrieval.QueryTerms("How do I get the PTO days?")
	want := []string{"pto", "days"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("terms = %v, want %v", got, want)
	}
}

func TestQueryTermsDropsSingleCharacterTokens(t *testing.T) {
	got := retrieval.QueryTerms("x y leave")
	if !reflect.DeepEqual(got, []string{"leave"}) {
		t.Fatalf("terms = %v", got)
	}
}

func TestQueryTermsDedupes(t *testing.T) {
	got := retrieval.QueryTerms("pto PTO pto accrual")
	if !reflect.DeepEqual(got, []string{"pto", "accrual"}) {
		t.Fatalf("terms = %v", got)
	}
}

func TestQueryTermsStripsTsqueryOperators(t *testing.T) {
	// Operators must never reach to_tsquery: only [a-z0-9] survives.
	got := retrieval.QueryTerms("pto & (drop | table) ! :* <-> 'x'")
	for _, term := range got {
		for _, r := range term {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') {
				t.Fatalf("term %q contains a non-alphanumeric rune", term)
			}
		}
	}
	if !reflect.DeepEqual(got, []string{"pto", "drop", "table"}) {
		t.Fatalf("terms = %v", got)
	}
}

func TestQueryTermsEmptyAndStopwordOnly(t *testing.T) {
	if got := retrieval.QueryTerms(""); len(got) != 0 {
		t.Fatalf("empty query terms = %v", got)
	}
	if got := retrieval.QueryTerms("what is the"); len(got) != 0 {
		t.Fatalf("stopword-only terms = %v", got)
	}
}

func TestQueryTermsKeepsDigits(t *testing.T) {
	got := retrieval.QueryTerms("401k match")
	if !reflect.DeepEqual(got, []string{"401k", "match"}) {
		t.Fatalf("terms = %v", got)
	}
}
