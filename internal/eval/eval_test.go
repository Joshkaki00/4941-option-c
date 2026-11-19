package eval

import (
	"strings"
	"testing"
)

func TestRecallAt5(t *testing.T) {
	top5 := []string{"a.md", "b.md", "c.md"}
	if !Recall5([]string{"c.md"}, top5) {
		t.Fatal("gold in top 5 must count as recall")
	}
	if Recall5([]string{"z.md"}, top5) {
		t.Fatal("gold absent must not count")
	}
	if Recall5(nil, top5) {
		t.Fatal("no gold means no recall")
	}
}

func TestCorrectDecline(t *testing.T) {
	cases := []struct {
		name string
		obs  Observed
		want bool
	}{
		{"declined with I don't know", Observed{Outcome: "declined_no_source", Answer: "I don't know — ..."}, true},
		{"curly apostrophe accepted", Observed{Outcome: "declined_no_source", Answer: "I don’t know."}, true},
		{"routed to human with standard copy", Observed{Outcome: "routed_to_human", Answer: "Please contact HR."}, true},
		{"answered is a failed decline", Observed{Outcome: "answered", Answer: "15 days [1]"}, false},
		{"declined without the copy", Observed{Outcome: "declined_no_source", Answer: "Nope."}, false},
		{"invented answer with decline words", Observed{Outcome: "answered", Answer: "I don't know, but 20 days [1]"}, false},
	}
	for _, c := range cases {
		if got := CorrectDecline(c.obs); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestSummarizeGates(t *testing.T) {
	items := []Item{
		{ID: "G01", ExpectOutcome: "answered", GoldSourceIDs: []string{"pto.md"}},
		{ID: "G02", ExpectOutcome: "answered", GoldSourceIDs: []string{"sick.md"}},
		{ID: "G06", ExpectOutcome: "declined_no_source"},
		{ID: "G07", ExpectOutcome: "routed_to_human"},
	}
	obs := []Observed{
		{Outcome: "answered", Answer: "x", RetrievedSourceIDs: []string{"pto.md"}},
		{Outcome: "answered", Answer: "x", RetrievedSourceIDs: []string{"other.md"}},
		{Outcome: "declined_no_source", Answer: "I don't know"},
		{Outcome: "routed_to_human", Answer: "contact HR"},
	}
	r := Summarize(items, obs)
	if r.AnswerableN != 2 || r.RecallHits != 1 {
		t.Fatalf("recall counts: %+v", r)
	}
	if r.DeclineN != 2 || r.DeclineHits != 2 {
		t.Fatalf("decline counts: %+v", r)
	}
	if r.RecallPass() {
		t.Fatal("recall 0.5 must fail the 0.8 gate")
	}
	if !r.DeclinePass() {
		t.Fatal("decline 1.0 must pass the 0.95 gate")
	}
	if r.Pass() {
		t.Fatal("overall must fail when recall fails")
	}
}

func TestSummarizeFailsWhenNoAnswerableItems(t *testing.T) {
	items := []Item{{ID: "G06", ExpectOutcome: "declined_no_source"}}
	r := Summarize(items, []Observed{{Outcome: "declined_no_source", Answer: "I don't know"}})
	if r.Pass() {
		t.Fatal("a run with no answerable items must not pass")
	}
}

func TestValidateRequiresDeclineItem(t *testing.T) {
	set := Set{Version: "v1", Items: []Item{{ID: "G01", Question: "q", ExpectOutcome: "answered", GoldSourceIDs: []string{"a.md"}}}}
	if err := set.Validate(); err == nil || !strings.Contains(err.Error(), "decline") {
		t.Fatalf("fixture without a decline-class item must be rejected, got %v", err)
	}
}

func TestValidateRejectsBadOutcomeAndMissingGold(t *testing.T) {
	bad := Set{Items: []Item{
		{ID: "G01", Question: "q", ExpectOutcome: "maybe"},
		{ID: "G06", Question: "q", ExpectOutcome: "declined_no_source"},
	}}
	if err := bad.Validate(); err == nil {
		t.Fatal("unknown expect_outcome must fail validation")
	}
	noGold := Set{Items: []Item{
		{ID: "G01", Question: "q", ExpectOutcome: "answered"},
		{ID: "G06", Question: "q", ExpectOutcome: "declined_no_source"},
	}}
	if err := noGold.Validate(); err == nil {
		t.Fatal("answerable item without gold_source_ids must fail validation")
	}
}

func TestParseSet(t *testing.T) {
	raw := `{"version":"v1","items":[{"id":"G06","question":"espresso?","audiences":["all-employees"],"expect_outcome":"declined_no_source"}]}`
	set, err := ParseSet([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if set.Version != "v1" || len(set.Items) != 1 || set.Items[0].ID != "G06" {
		t.Fatalf("bad parse: %+v", set)
	}
	if _, err := ParseSet([]byte("{")); err == nil {
		t.Fatal("bad json must fail")
	}
}
