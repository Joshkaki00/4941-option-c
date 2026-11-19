// Command eval runs the frozen golden set through the live pipeline and enforces the quality gates.
//
//	go run ./cmd/eval -set testdata/golden/v1.json
//	go run ./cmd/eval -set testdata/golden/v1.json -retrieval-only   # recall only, no LLM
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Joshkaki00/4941-option-c/internal/app"
	"github.com/Joshkaki00/4941-option-c/internal/config"
	"github.com/Joshkaki00/4941-option-c/internal/eval"
)

func main() {
	code, err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "eval:", err)
	}
	os.Exit(code)
}

func run() (int, error) {
	setPath := flag.String("set", "testdata/golden/v1.json", "golden set file")
	retrievalOnly := flag.Bool("retrieval-only", false, "score recall@5 only; skip generation and decline gates")
	flag.Parse()

	raw, err := os.ReadFile(*setPath)
	if err != nil {
		return 2, err
	}
	set, err := eval.ParseSet(raw)
	if err != nil {
		return 2, err
	}

	if err := config.LoadDotEnv(".env"); err != nil {
		return 2, err
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return 2, err
	}
	if !*retrievalOnly && cfg.LLMProvider == "none" {
		return 2, fmt.Errorf("LLM_PROVIDER=none: run with -retrieval-only, or set ANTHROPIC_API_KEY")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	rt, err := app.Open(ctx, cfg)
	if err != nil {
		return 2, err
	}
	defer rt.Close()

	obs := make([]eval.Observed, len(set.Items))
	for i, it := range set.Items {
		o, err := observe(ctx, rt, it, *retrievalOnly)
		if err != nil {
			return 2, fmt.Errorf("%s: %w", it.ID, err)
		}
		obs[i] = o
		fmt.Printf("%-4s expect=%-20s got=%-20s retrieved=%v\n", it.ID, it.ExpectOutcome, o.Outcome, o.RetrievedSourceIDs)
	}

	report := eval.Summarize(set.Items, obs)
	fmt.Printf("\nrecall@5   %d/%d = %.2f (gate >= %.2f)\n", report.RecallHits, report.AnswerableN, report.Recall(), eval.MinRecallAt5)
	if !*retrievalOnly {
		fmt.Printf("decline    %d/%d = %.2f (gate >= %.2f)\n", report.DeclineHits, report.DeclineN, report.CorrectDeclineRate(), eval.MinCorrectDecline)
		fmt.Printf("outcome match %d/%d\n", report.OutcomeMatch, len(set.Items))
	}
	for _, f := range report.Failures {
		fmt.Println("  -", f)
	}

	if *retrievalOnly {
		if !report.RecallPass() {
			return 1, fmt.Errorf("recall@5 gate failed")
		}
		return 0, nil
	}
	if !report.Pass() {
		return 1, fmt.Errorf("quality gate failed")
	}
	fmt.Println("PASS")
	return 0, nil
}

func observe(ctx context.Context, rt *app.Runtime, it eval.Item, retrievalOnly bool) (eval.Observed, error) {
	if retrievalOnly {
		res, err := rt.Searcher.Hybrid(ctx, it.Audiences, it.Question, rt.Config.TopK)
		if err != nil {
			return eval.Observed{}, err
		}
		o := eval.Observed{Outcome: "retrieval-only"}
		for _, c := range res.Candidates {
			o.RetrievedSourceIDs = append(o.RetrievedSourceIDs, c.SourceID)
		}
		return o, nil
	}

	// Ask the full pipeline. Generated text goes to the report only.
	res, err := rt.Answers.Ask(ctx, answerRequest(it), nil)
	if err != nil {
		return eval.Observed{}, err
	}
	o := eval.Observed{Outcome: string(res.Outcome), Answer: res.Answer}
	for _, c := range res.Retrieved {
		o.RetrievedSourceIDs = append(o.RetrievedSourceIDs, c.SourceID)
	}
	return o, nil
}
