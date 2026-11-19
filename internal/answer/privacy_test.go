package answer

import (
	"strings"
	"testing"
	"time"

	"github.com/Joshkaki00/4941-option-c/internal/embed"
	"github.com/Joshkaki00/4941-option-c/internal/llm"
	"github.com/Joshkaki00/4941-option-c/internal/retrieval"
	"github.com/Joshkaki00/4941-option-c/internal/store"
	"github.com/Joshkaki00/4941-option-c/internal/testutil"
)

const restrictedMarker = "executive bonus pool allocation"

// TestAudienceFilterBeforeLLM is the answer-level half of Gate 3 (spec AC4).
// It uses real retrieval and a recording generator. The restricted text must
// never appear in any prompt sent to the LLM.
func TestAudienceFilterBeforeLLM(t *testing.T) {
	st := testutil.OpenDB(t)
	emb := embed.NewHash(store.Dim)
	testutil.Seed(t, st, emb, testutil.Doc{
		SourceID:  "pto-policy.md",
		Title:     "PTO Policy",
		Audiences: []string{"all-employees"},
		Body:      "# PTO Policy\n\n## Allotment\n\nFull-time employees receive 15 vacation days.\n",
	})
	testutil.Seed(t, st, emb, testutil.Doc{
		SourceID:  "compensation-bands.md",
		Title:     "Compensation Bands",
		Audiences: []string{"hr-only"},
		Body:      "# Compensation Bands\n\n## Bonus\n\nThe " + restrictedMarker + " is set each year.\n",
	})

	gen := &llm.Fake{Reply: "The handbook does not say [1]."}
	svc := &Service{
		Retriever:       retrieval.New(st, emb),
		Generator:       gen,
		Model:           "test",
		TopK:            5,
		MinSim:          0.1,
		RetrieveTimeout: 5 * time.Second,
		Handoff:         "Contact HR.",
	}

	res, err := svc.Ask(t.Context(), Request{
		Question:  "How is the executive bonus pool allocation decided?",
		Audiences: []string{"all-employees"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range res.Retrieved {
		if c.SourceID == "compensation-bands.md" {
			t.Fatalf("restricted document was retrieved for an employee: %+v", c)
		}
	}
	for _, call := range gen.Calls() {
		if strings.Contains(call.User, restrictedMarker) || strings.Contains(call.System, restrictedMarker) {
			t.Fatalf("restricted text was sent to the LLM: %q", call.User)
		}
	}
}
