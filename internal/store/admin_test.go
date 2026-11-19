package store_test

import (
	"errors"
	"testing"

	"github.com/Joshkaki00/4941-option-c/internal/store"
	"github.com/Joshkaki00/4941-option-c/internal/testutil"
)

func TestGetJobReturnsQueuedStatus(t *testing.T) {
	st := testutil.OpenDB(t)
	ctx := t.Context()

	id, err := st.EnqueueIngestion(ctx, "pto-policy.md")
	if err != nil {
		t.Fatal(err)
	}
	job, err := st.GetJob(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != id || job.SourcePath != "pto-policy.md" || job.Status != "queued" || job.Attempts != 0 {
		t.Fatalf("job = %+v", job)
	}
}

func TestGetJobMissingIsErrNotFound(t *testing.T) {
	st := testutil.OpenDB(t)
	_, err := st.GetJob(t.Context(), 987654)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// seedMessages stores one conversation with the given outcomes, one message per outcome.
func seedMessages(t *testing.T, st *store.Store, outcomes map[string]int) {
	t.Helper()
	ctx := t.Context()
	convID, err := st.CreateConversation(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	for outcome, n := range outcomes {
		for i := 0; i < n; i++ {
			_, err := st.InsertMessage(ctx, store.MessageRow{
				ConversationID: convID, UserSub: "alice", Question: "q " + outcome,
				Answer: "a", Outcome: outcome,
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestOutcomeSummaryCountsEachOutcome(t *testing.T) {
	st := testutil.OpenDB(t)
	seedMessages(t, st, map[string]int{"answered": 3, "declined_no_source": 2, "routed_to_human": 1})

	sum, err := st.OutcomeSummary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if sum["answered"] != 3 || sum["declined_no_source"] != 2 || sum["routed_to_human"] != 1 {
		t.Fatalf("summary = %v", sum)
	}
	if sum["clarification_needed"] != 0 {
		t.Fatalf("unseen outcome must report 0, got %d", sum["clarification_needed"])
	}
}

func TestOutcomeSummaryEmptyIsZero(t *testing.T) {
	st := testutil.OpenDB(t)
	sum, err := st.OutcomeSummary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for outcome, n := range sum {
		if n != 0 {
			t.Fatalf("%s = %d on empty store", outcome, n)
		}
	}
}

func TestUnansweredQuestionsGroupsWithoutIdentities(t *testing.T) {
	st := testutil.OpenDB(t)
	seedMessages(t, st, map[string]int{"declined_no_source": 2, "answered": 5})

	rows, err := st.UnansweredQuestions(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one unanswered question group", rows)
	}
	if rows[0].Question != "q declined_no_source" || rows[0].Count != 2 {
		t.Fatalf("row = %+v", rows[0])
	}
}

func TestUnansweredQuestionsRespectsLimit(t *testing.T) {
	st := testutil.OpenDB(t)
	seedMessages(t, st, map[string]int{"declined_no_source": 1, "routed_to_human": 1})
	rows, err := st.UnansweredQuestions(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("limit 1 returned %d rows", len(rows))
	}
}
