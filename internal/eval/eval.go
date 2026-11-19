// Package eval scores the frozen golden set against the quality gates (spec.md Gate 2).
package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Gate thresholds from spec.md Gate 2.
const (
	MinRecallAt5      = 0.8
	MinCorrectDecline = 0.95
	topK              = 5
)

// Item is one golden question with its expected behavior.
type Item struct {
	ID            string   `json:"id"`
	Question      string   `json:"question"`
	Audiences     []string `json:"audiences"`
	ExpectOutcome string   `json:"expect_outcome"`
	GoldSourceIDs []string `json:"gold_source_ids,omitempty"`
	Notes         string   `json:"notes,omitempty"`
}

// Set is a versioned golden fixture.
type Set struct {
	Version string `json:"version"`
	Items   []Item `json:"items"`
}

// Observed is what the system produced for one item.
type Observed struct {
	Outcome            string
	Answer             string
	RetrievedSourceIDs []string // top-K in rank order
}

var validOutcomes = map[string]bool{
	"answered": true, "declined_no_source": true, "routed_to_human": true, "clarification_needed": true,
}

// ParseSet decodes a fixture and validates it.
func ParseSet(raw []byte) (Set, error) {
	var set Set
	if err := json.Unmarshal(raw, &set); err != nil {
		return Set{}, fmt.Errorf("parse golden set: %w", err)
	}
	if err := set.Validate(); err != nil {
		return Set{}, err
	}
	return set, nil
}

// Validate checks structure. A fixture must contain at least one decline-class item (spec Gate 2).
func (s Set) Validate() error {
	seen := map[string]bool{}
	declines := 0
	for _, it := range s.Items {
		if it.ID == "" || it.Question == "" {
			return errors.New("eval: every item needs id and question")
		}
		if seen[it.ID] {
			return fmt.Errorf("eval: duplicate id %s", it.ID)
		}
		seen[it.ID] = true
		if !validOutcomes[it.ExpectOutcome] {
			return fmt.Errorf("eval: %s has unknown expect_outcome %q", it.ID, it.ExpectOutcome)
		}
		if it.ExpectOutcome == "answered" && len(it.GoldSourceIDs) == 0 {
			return fmt.Errorf("eval: answerable item %s needs gold_source_ids", it.ID)
		}
		if IsDeclineClass(it.ExpectOutcome) {
			declines++
		}
	}
	if declines == 0 {
		return errors.New("eval: fixture must include at least one decline-class item")
	}
	return nil
}

// IsDeclineClass reports whether an expected outcome counts toward the decline gate.
func IsDeclineClass(outcome string) bool {
	return outcome == "declined_no_source" || outcome == "routed_to_human"
}

// Recall5 reports whether any gold source appears in the top-5 retrieved sources.
func Recall5(gold, top []string) bool {
	if len(top) > topK {
		top = top[:topK]
	}
	for _, g := range gold {
		for _, t := range top {
			if g == t {
				return true
			}
		}
	}
	return false
}

// CorrectDecline reports whether an observed result is a correct decline.
// It needs a decline-class outcome plus the standard copy ("I don't know"),
// or routed_to_human, which uses its own standard handoff copy.
func CorrectDecline(o Observed) bool {
	if !IsDeclineClass(o.Outcome) {
		return false
	}
	if o.Outcome == "routed_to_human" {
		return true
	}
	norm := strings.ToLower(strings.ReplaceAll(o.Answer, "’", "'"))
	return strings.Contains(norm, "i don't know")
}

// Report summarizes one run.
type Report struct {
	AnswerableN  int
	RecallHits   int
	DeclineN     int
	DeclineHits  int
	OutcomeMatch int
	Failures     []string
}

// Recall returns recall@5 over answerable items (0 when none).
func (r Report) Recall() float64 {
	if r.AnswerableN == 0 {
		return 0
	}
	return float64(r.RecallHits) / float64(r.AnswerableN)
}

// CorrectDeclineRate returns the decline-class pass rate (0 when none).
func (r Report) CorrectDeclineRate() float64 {
	if r.DeclineN == 0 {
		return 0
	}
	return float64(r.DeclineHits) / float64(r.DeclineN)
}

// RecallPass is the recall@5 gate.
func (r Report) RecallPass() bool { return r.AnswerableN > 0 && r.Recall() >= MinRecallAt5 }

// DeclinePass is the correct-decline gate.
func (r Report) DeclinePass() bool {
	return r.DeclineN > 0 && r.CorrectDeclineRate() >= MinCorrectDecline
}

// Pass is true only when both gates pass.
func (r Report) Pass() bool { return r.RecallPass() && r.DeclinePass() }

// Summarize scores items against observations (obs[i] belongs to items[i]).
func Summarize(items []Item, obs []Observed) Report {
	var r Report
	for i, it := range items {
		if i >= len(obs) {
			// A missing observation is a miss, not a skipped item: it must count in the denominator.
			r.Failures = append(r.Failures, it.ID+": no observation")
			if it.ExpectOutcome == "answered" {
				r.AnswerableN++
			}
			if IsDeclineClass(it.ExpectOutcome) {
				r.DeclineN++
			}
			continue
		}
		o := obs[i]
		if o.Outcome == it.ExpectOutcome {
			r.OutcomeMatch++
		}
		if it.ExpectOutcome == "answered" {
			r.AnswerableN++
			if Recall5(it.GoldSourceIDs, o.RetrievedSourceIDs) {
				r.RecallHits++
			} else {
				r.Failures = append(r.Failures, fmt.Sprintf("%s: gold %v not in top-%d", it.ID, it.GoldSourceIDs, topK))
			}
		}
		if IsDeclineClass(it.ExpectOutcome) {
			r.DeclineN++
			if CorrectDecline(o) {
				r.DeclineHits++
			} else {
				r.Failures = append(r.Failures, fmt.Sprintf("%s: expected decline, got outcome=%s", it.ID, o.Outcome))
			}
		}
	}
	return r
}
