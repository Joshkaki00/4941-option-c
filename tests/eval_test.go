package tests

import (
	"strings"
	"testing"

	"github.com/Joshkaki00/4941-option-c/internal/eval"
)

func TestParseSetAcceptsValidFixture(t *testing.T) {
	raw := `{"version":"v1","items":[
		{"id":"G01","question":"How many PTO days?","expect_outcome":"answered","gold_source_ids":["pto.md"]},
		{"id":"G05","question":"Espresso machine?","expect_outcome":"declined_no_source"}
	]}`
	set, err := eval.ParseSet([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Items) != 2 {
		t.Fatalf("items = %d", len(set.Items))
	}
}

func TestParseSetRejectsBadJSON(t *testing.T) {
	if _, err := eval.ParseSet([]byte("{not json")); err == nil {
		t.Fatal("bad JSON must fail")
	}
}

func TestValidateRejectsStructuralProblems(t *testing.T) {
	decline := eval.Item{ID: "D1", Question: "q", ExpectOutcome: "declined_no_source"}
	cases := map[string]eval.Set{
		"missing id": {Items: []eval.Item{{Question: "q", ExpectOutcome: "answered", GoldSourceIDs: []string{"a"}}, decline}},
		"missing question": {Items: []eval.Item{
			{ID: "A", ExpectOutcome: "answered", GoldSourceIDs: []string{"a"}}, decline}},
		"duplicate id": {Items: []eval.Item{
			{ID: "A", Question: "q", ExpectOutcome: "answered", GoldSourceIDs: []string{"a"}},
			{ID: "A", Question: "q", ExpectOutcome: "declined_no_source"}}},
		"unknown outcome": {Items: []eval.Item{
			{ID: "A", Question: "q", ExpectOutcome: "maybe"}, decline}},
		"answered without gold": {Items: []eval.Item{
			{ID: "A", Question: "q", ExpectOutcome: "answered"}, decline}},
		"no decline item": {Items: []eval.Item{
			{ID: "A", Question: "q", ExpectOutcome: "answered", GoldSourceIDs: []string{"a"}}}},
	}
	for name, set := range cases {
		if err := set.Validate(); err == nil {
			t.Errorf("%s: want validation error", name)
		}
	}
}

func TestIsDeclineClass(t *testing.T) {
	for outcome, want := range map[string]bool{
		"declined_no_source":   true,
		"routed_to_human":      true,
		"answered":             false,
		"clarification_needed": false,
		"":                     false,
	} {
		if got := eval.IsDeclineClass(outcome); got != want {
			t.Errorf("IsDeclineClass(%q) = %v, want %v", outcome, got, want)
		}
	}
}

func TestRecall5OnlyLooksAtTopFive(t *testing.T) {
	top := []string{"a", "b", "c", "d", "e", "gold"}
	if eval.Recall5([]string{"gold"}, top) {
		t.Fatal("gold at rank 6 must not count as recall@5")
	}
	if !eval.Recall5([]string{"x", "c"}, top) {
		t.Fatal("any gold source in top 5 counts")
	}
}

func TestRecall5EmptyInputs(t *testing.T) {
	if eval.Recall5(nil, []string{"a"}) {
		t.Fatal("no gold means no recall")
	}
	if eval.Recall5([]string{"a"}, nil) {
		t.Fatal("no retrieval means no recall")
	}
}

func TestCorrectDeclineNeedsDeclineOutcomeAndCopy(t *testing.T) {
	cases := []struct {
		name string
		o    eval.Observed
		want bool
	}{
		{"declined with standard copy", eval.Observed{Outcome: "declined_no_source", Answer: "I don't know. Try HR."}, true},
		{"declined with curly apostrophe", eval.Observed{Outcome: "declined_no_source", Answer: "I don’t know."}, true},
		{"declined with uppercase copy", eval.Observed{Outcome: "declined_no_source", Answer: "I DON'T KNOW"}, true},
		{"declined without standard copy", eval.Observed{Outcome: "declined_no_source", Answer: "No info."}, false},
		{"routed always counts", eval.Observed{Outcome: "routed_to_human", Answer: ""}, true},
		{"answered is never a correct decline", eval.Observed{Outcome: "answered", Answer: "I don't know"}, false},
	}
	for _, tc := range cases {
		if got := eval.CorrectDecline(tc.o); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestReportRatesAndZeroDenominators(t *testing.T) {
	var r eval.Report
	if r.Recall() != 0 || r.CorrectDeclineRate() != 0 {
		t.Fatal("empty report must return zero rates, not NaN")
	}
	if r.RecallPass() || r.DeclinePass() || r.Pass() {
		t.Fatal("empty report must not pass any gate")
	}
}

func TestReportGateThresholdsAreInclusive(t *testing.T) {
	// 4/5 = 0.8 exactly passes recall; 19/20 = 0.95 exactly passes declines.
	r := eval.Report{AnswerableN: 5, RecallHits: 4, DeclineN: 20, DeclineHits: 19}
	if !r.RecallPass() {
		t.Fatal("recall 0.8 must pass the >= 0.8 gate")
	}
	if !r.DeclinePass() {
		t.Fatal("decline 0.95 must pass the >= 0.95 gate")
	}
	if !r.Pass() {
		t.Fatal("both gates pass, Pass must be true")
	}
}

func TestReportFailsWhenEitherGateFails(t *testing.T) {
	recallFails := eval.Report{AnswerableN: 5, RecallHits: 3, DeclineN: 1, DeclineHits: 1}
	if recallFails.Pass() {
		t.Fatal("recall 0.6 must fail overall")
	}
	declineFails := eval.Report{AnswerableN: 5, RecallHits: 5, DeclineN: 20, DeclineHits: 18}
	if declineFails.Pass() {
		t.Fatal("decline 0.9 must fail overall")
	}
}

func TestSummarizeScoresAndRecordsFailures(t *testing.T) {
	items := []eval.Item{
		{ID: "A", Question: "q", ExpectOutcome: "answered", GoldSourceIDs: []string{"pto.md"}},
		{ID: "B", Question: "q", ExpectOutcome: "declined_no_source"},
		{ID: "C", Question: "q", ExpectOutcome: "answered", GoldSourceIDs: []string{"x.md"}},
	}
	obs := []eval.Observed{
		{Outcome: "answered", RetrievedSourceIDs: []string{"pto.md"}},
		{Outcome: "answered", Answer: "I don't know"}, // wrong outcome for a decline item
		// C has no observation at all
	}
	r := eval.Summarize(items, obs)
	if r.AnswerableN != 2 || r.RecallHits != 1 {
		t.Fatalf("recall counts = %d/%d", r.RecallHits, r.AnswerableN)
	}
	if r.DeclineN != 1 || r.DeclineHits != 0 {
		t.Fatalf("decline counts = %d/%d", r.DeclineHits, r.DeclineN)
	}
	if r.OutcomeMatch != 1 {
		t.Fatalf("outcome matches = %d, want 1", r.OutcomeMatch)
	}
	joined := strings.Join(r.Failures, "\n")
	for _, want := range []string{"B: expected decline", "C: no observation"} {
		if !strings.Contains(joined, want) {
			t.Errorf("failures missing %q:\n%s", want, joined)
		}
	}
}
