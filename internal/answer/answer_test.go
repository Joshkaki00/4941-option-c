package answer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Joshkaki00/4941-option-c/internal/llm"
	"github.com/Joshkaki00/4941-option-c/internal/retrieval"
)

type fakeRetriever struct {
	res      retrieval.Result
	err      error
	calls    int
	gotAud   []string
	gotLimit int
}

func (f *fakeRetriever) Hybrid(_ context.Context, aud []string, _ string, limit int) (retrieval.Result, error) {
	f.calls++
	f.gotAud = aud
	f.gotLimit = limit
	return f.res, f.err
}

func ptoCandidates() []retrieval.Candidate {
	return []retrieval.Candidate{
		{
			ChunkID: "c1", VersionID: 1, SourceID: "pto-policy.md", Title: "PTO Policy",
			HeadingPath: []string{"Allotment"}, Content: "PTO Policy > Allotment\n\nFull-time employees receive 15 days.",
			LastUpdated: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), Score: 0.8,
		},
		{
			ChunkID: "c2", VersionID: 1, SourceID: "sick-leave.md", Title: "Sick Leave",
			HeadingPath: nil, Content: "Sick Leave\n\nSick days are separate from PTO.",
			LastUpdated: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), Score: 0.4,
		},
	}
}

func strongRetrieval() retrieval.Result {
	return retrieval.Result{Candidates: ptoCandidates(), KeywordHits: 2, BestSim: 0.8}
}

func newService(r Retriever, g llm.Generator) *Service {
	return &Service{
		Retriever:       r,
		Generator:       g,
		Model:           "test-model",
		TopK:            5,
		MinSim:          0.5,
		RetrieveTimeout: time.Second,
		Handoff:         "Contact HR.",
	}
}

var employee = []string{"all-employees"}

func TestAskAnsweredWithCitationsAndStreaming(t *testing.T) {
	r := &fakeRetriever{res: strongRetrieval()}
	g := &llm.Fake{Reply: "Full-time employees receive 15 days [1]."}
	svc := newService(r, g)

	var streamed strings.Builder
	res, err := svc.Ask(context.Background(), Request{Question: "How many vacation days do I get?", Audiences: employee},
		func(s string) error { streamed.WriteString(s); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != Answered {
		t.Fatalf("want answered, got %s", res.Outcome)
	}
	if streamed.String() != g.Reply {
		t.Fatalf("streamed text %q != reply %q", streamed.String(), g.Reply)
	}
	used := usedCitations(res)
	if len(used) != 1 || used[0].DocumentTitle != "PTO Policy" || used[0].Section != "Allotment" {
		t.Fatalf("want one used citation for PTO Policy > Allotment, got %+v", used)
	}
	if used[0].LastUpdated.IsZero() {
		t.Fatal("citation must carry last-updated date")
	}
	if r.gotLimit != 5 {
		t.Fatalf("retriever limit should equal TopK, got %d", r.gotLimit)
	}
}

func TestAskPassesCallerAudiencesToRetrieval(t *testing.T) {
	r := &fakeRetriever{res: strongRetrieval()}
	svc := newService(r, &llm.Fake{Reply: "15 days [1]."})
	if _, err := svc.Ask(context.Background(), Request{Question: "vacation", Audiences: []string{"all-employees", "us-ca"}}, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Join(r.gotAud, ",") != "all-employees,us-ca" {
		t.Fatalf("audiences not passed through: %v", r.gotAud)
	}
}

func TestAskNoCandidatesDeclinesWithoutCallingLLM(t *testing.T) {
	r := &fakeRetriever{}
	g := &llm.Fake{Reply: "should not be used"}
	svc := newService(r, g)

	res, err := svc.Ask(context.Background(), Request{Question: "Can I get reimbursed for a home espresso machine?", Audiences: employee}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != DeclinedNoSource {
		t.Fatalf("want declined_no_source, got %s", res.Outcome)
	}
	if !strings.Contains(res.Answer, "I don't know") {
		t.Fatalf("answer must say I don't know, got %q", res.Answer)
	}
	if res.Handoff == "" {
		t.Fatal("decline must include a human handoff")
	}
	if n := len(g.Calls()); n != 0 {
		t.Fatalf("LLM must not be called on empty retrieval, got %d calls", n)
	}
}

func TestAskWeakRetrievalDeclinesWithoutCallingLLM(t *testing.T) {
	r := &fakeRetriever{res: retrieval.Result{Candidates: ptoCandidates(), KeywordHits: 0, BestSim: 0.2}}
	g := &llm.Fake{Reply: "should not be used"}
	svc := newService(r, g)

	res, err := svc.Ask(context.Background(), Request{Question: "espresso machine", Audiences: employee}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != DeclinedNoSource {
		t.Fatalf("weak retrieval must decline, got %s", res.Outcome)
	}
	if n := len(g.Calls()); n != 0 {
		t.Fatalf("LLM must not be called on weak retrieval, got %d calls", n)
	}
}

func TestAskModelDeclineIsDeclinedNoSource(t *testing.T) {
	r := &fakeRetriever{res: strongRetrieval()}
	g := &llm.Fake{Reply: "I don't know"}
	svc := newService(r, g)

	res, err := svc.Ask(context.Background(), Request{Question: "espresso reimbursement", Audiences: employee}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != DeclinedNoSource {
		t.Fatalf("want declined_no_source, got %s", res.Outcome)
	}
	if len(usedCitations(res)) != 0 {
		t.Fatal("a decline must not present citations as authority")
	}
}

func TestAskUncitedAnswerBecomesDeclined(t *testing.T) {
	r := &fakeRetriever{res: strongRetrieval()}
	g := &llm.Fake{Reply: "Employees get 15 days."} // no [n] marker
	svc := newService(r, g)

	res, err := svc.Ask(context.Background(), Request{Question: "vacation", Audiences: employee}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != DeclinedNoSource {
		t.Fatalf("ungrounded answer must not be returned as answered, got %s", res.Outcome)
	}
}

func TestAskOutOfRangeCitationMarkerIsIgnored(t *testing.T) {
	r := &fakeRetriever{res: strongRetrieval()} // 2 passages
	g := &llm.Fake{Reply: "Employees get 15 days [9]."}
	svc := newService(r, g)

	res, err := svc.Ask(context.Background(), Request{Question: "vacation", Audiences: employee}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != DeclinedNoSource {
		t.Fatalf("citation [9] with 2 passages must not count, got %s", res.Outcome)
	}
}

func TestAskSensitiveTopicRoutesToHumanWithoutRetrieval(t *testing.T) {
	r := &fakeRetriever{res: strongRetrieval()}
	g := &llm.Fake{Reply: "should not be used"}
	svc := newService(r, g)

	res, err := svc.Ask(context.Background(), Request{Question: "Should I sue my manager?", Audiences: employee}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != RoutedToHuman {
		t.Fatalf("want routed_to_human, got %s", res.Outcome)
	}
	if r.calls != 0 || len(g.Calls()) != 0 {
		t.Fatalf("sensitive topic must not retrieve or generate (retrieve=%d, llm=%d)", r.calls, len(g.Calls()))
	}
}

func TestAskPersonalBalanceDeclines(t *testing.T) {
	r := &fakeRetriever{res: strongRetrieval()}
	g := &llm.Fake{Reply: "should not be used"}
	svc := newService(r, g)

	res, err := svc.Ask(context.Background(), Request{Question: "How many vacation days do I have left this year?", Audiences: employee}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != DeclinedNoSource {
		t.Fatalf("want declined_no_source, got %s", res.Outcome)
	}
	if !strings.Contains(res.Answer, "personal balances") {
		t.Fatalf("answer should say the handbook cannot answer personal balances, got %q", res.Answer)
	}
	if r.calls != 0 || len(g.Calls()) != 0 {
		t.Fatal("personal balance question must not reach retrieval or the LLM")
	}
}

func TestAskGeneralPolicyQuestionIsNotMistakenForPersonal(t *testing.T) {
	if IsPersonal("How many vacation days do I get?") {
		t.Fatal("entitlement question must not be classified as personal balance")
	}
	if !IsPersonal("How many vacation days do I have left?") {
		t.Fatal("balance question must be classified as personal")
	}
}

func TestAskRetrievalOnlyTierShowsTopPassages(t *testing.T) {
	r := &fakeRetriever{res: strongRetrieval()}
	svc := newService(r, nil) // no generator configured

	res, err := svc.Ask(context.Background(), Request{Question: "vacation", Audiences: employee}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != RoutedToHuman {
		t.Fatalf("retrieval-only tier must route to a human, got %s", res.Outcome)
	}
	if len(res.Citations) == 0 {
		t.Fatal("retrieval-only tier must still show source passages")
	}
}

func TestAskGeneratorErrorIsReturned(t *testing.T) {
	boom := errors.New("upstream 503")
	svc := newService(&fakeRetriever{res: strongRetrieval()}, &llm.Fake{Err: boom})
	if _, err := svc.Ask(context.Background(), Request{Question: "vacation", Audiences: employee}, nil); !errors.Is(err, boom) {
		t.Fatalf("want wrapped generator error, got %v", err)
	}
}

func TestAskRetrieverErrorIsReturned(t *testing.T) {
	boom := errors.New("db down")
	svc := newService(&fakeRetriever{err: boom}, &llm.Fake{Reply: "x [1]"})
	if _, err := svc.Ask(context.Background(), Request{Question: "vacation", Audiences: employee}, nil); !errors.Is(err, boom) {
		t.Fatalf("want wrapped retriever error, got %v", err)
	}
}

func TestAskStopsWhenClientDisconnects(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	svc := newService(&fakeRetriever{res: strongRetrieval()}, &llm.Fake{Reply: "Employees get 15 days [1] and more words here."})
	_, err := svc.Ask(ctx, Request{Question: "vacation", Audiences: employee}, func(string) error {
		cancel()
		return ctx.Err()
	})
	if err == nil {
		t.Fatal("cancelled stream must return an error, not complete")
	}
}

func TestPromptContainsOnlyTheRetrievedPassages(t *testing.T) {
	g := &llm.Fake{Reply: "15 days [1]."}
	svc := newService(&fakeRetriever{res: strongRetrieval()}, g)
	if _, err := svc.Ask(context.Background(), Request{Question: "vacation", Audiences: employee}, nil); err != nil {
		t.Fatal(err)
	}
	calls := g.Calls()
	if len(calls) != 1 {
		t.Fatalf("want one LLM call, got %d", len(calls))
	}
	user := calls[0].User
	if !strings.Contains(user, "[1] PTO Policy > Allotment") || !strings.Contains(user, "[2] Sick Leave") {
		t.Fatalf("passages must be numbered with titles: %q", user)
	}
	if !strings.Contains(calls[0].System, "ONLY") {
		t.Fatal("system prompt must restrict answers to the passages")
	}
}

// Keyword presence on a common word ("home") must not count as relevant evidence.
func TestAskCommonWordKeywordMatchIsStillWeak(t *testing.T) {
	cands := []retrieval.Candidate{{
		ChunkID: "it", Title: "IT Password Reset", Content: "IT Password Reset\n\nOpen the self-service portal from the intranet home page.",
		LastUpdated: time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC), Score: 0.02,
	}}
	r := &fakeRetriever{res: retrieval.Result{Candidates: cands, KeywordHits: 1, BestSim: 0.1}}
	g := &llm.Fake{Reply: "should not be used"}
	svc := newService(r, g)

	res, err := svc.Ask(context.Background(), Request{Question: "Can I get reimbursed for a home espresso machine?", Audiences: employee}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != DeclinedNoSource {
		t.Fatalf("want declined_no_source, got %s", res.Outcome)
	}
	if n := len(g.Calls()); n != 0 {
		t.Fatalf("weak evidence must not reach the LLM, got %d calls", n)
	}
}

func TestAskWellCoveredKeywordMatchReachesLLM(t *testing.T) {
	cands := []retrieval.Candidate{{
		ChunkID: "pto", Title: "PTO Policy", HeadingPath: []string{"Allotment"},
		Content:     "PTO Policy > Allotment\n\nFull-time employees receive 15 vacation days each year.",
		LastUpdated: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), Score: 0.03,
	}}
	r := &fakeRetriever{res: retrieval.Result{Candidates: cands, KeywordHits: 1, BestSim: 0.1}}
	g := &llm.Fake{Reply: "15 vacation days [1]."}
	svc := newService(r, g)

	res, err := svc.Ask(context.Background(), Request{Question: "How many vacation days do full-time employees get?", Audiences: employee}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != Answered {
		t.Fatalf("well-covered evidence should answer, got %s", res.Outcome)
	}
}

func TestTermCoverageMatchesStemmedPrefixes(t *testing.T) {
	cands := []retrieval.Candidate{{Content: "Reimbursement is limited to approved vendors."}}
	if got := bestCoverage("reimbursed approved", cands); got != 1 {
		t.Fatalf("reimbursed~reimbursement and approved should both match, got %.2f", got)
	}
	if got := bestCoverage("espresso machine", cands); got != 0 {
		t.Fatalf("unrelated terms must score 0, got %.2f", got)
	}
	if got := bestCoverage("the and of", cands); got != 0 {
		t.Fatalf("stopword-only question must score 0, got %.2f", got)
	}
}

func TestIsSensitive(t *testing.T) {
	for _, q := range []string{"Should I sue my manager?", "I think I was discriminated against", "medical leave for a diagnosis"} {
		if !IsSensitive(q) {
			t.Errorf("want sensitive: %q", q)
		}
	}
	if IsSensitive("How do I reset my laptop password?") {
		t.Error("IT question must not be sensitive")
	}
}

func usedCitations(r Result) []Citation {
	var out []Citation
	for _, c := range r.Retrieved {
		if c.Used {
			out = append(out, c)
		}
	}
	return out
}
