package tests

import (
	"strings"
	"testing"
	"time"

	"github.com/Joshkaki00/4941-option-c/internal/answer"
	"github.com/Joshkaki00/4941-option-c/internal/retrieval"
)

func TestIsSensitiveFlagsLegalMedicalAndDisciplinary(t *testing.T) {
	for _, q := range []string{
		"Can I sue my manager?",
		"Do I need a LAWYER for this",
		"I was fired last week",
		"Is there a medical leave policy",
		"What is the harassment policy",
		"How does disciplinary action work",
	} {
		if !answer.IsSensitive(q) {
			t.Errorf("want sensitive: %q", q)
		}
	}
}

func TestIsSensitiveIgnoresOrdinaryQuestions(t *testing.T) {
	for _, q := range []string{
		"How many PTO days do full-time employees get?",
		"How do I reset my password?",
		"What is the expense policy for travel?",
	} {
		if answer.IsSensitive(q) {
			t.Errorf("should not be sensitive: %q", q)
		}
	}
}

func TestIsSensitiveRespectsWordBoundaries(t *testing.T) {
	// "fire" alone is not "fired"; "medicare" is not "medical".
	if answer.IsSensitive("fire drill procedure") {
		t.Error("fire drill is not a termination question")
	}
}

func TestIsPersonalNeedsFirstPersonAndBalanceWord(t *testing.T) {
	cases := map[string]bool{
		"How many PTO days do I have left?":         true,
		"What's my remaining balance?":              true,
		"How many sick days have I used so far?":    true,
		"How many PTO days do employees get?":       false, // policy, not personal
		"My laptop is broken":                       false, // first person, no balance word
		"What is the remaining budget for the team": false, // balance word, no first person
	}
	for q, want := range cases {
		if got := answer.IsPersonal(q); got != want {
			t.Errorf("IsPersonal(%q) = %v, want %v", q, got, want)
		}
	}
}

func TestBuildUserPromptNumbersAndLabelsPassages(t *testing.T) {
	when := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	cands := []retrieval.Candidate{
		{Title: "PTO Policy", HeadingPath: []string{"Accrual"}, Content: "Full-time: 15 days.", LastUpdated: when},
		{Title: "Sick Leave", Content: "Five days.", LastUpdated: when},
	}
	got := answer.BuildUserPrompt("How many days?", cands)

	for _, want := range []string{
		"<passages>",
		"[1] PTO Policy > Accrual\nFull-time: 15 days.",
		"[2] Sick Leave\nFive days.",
		"</passages>",
		"Question: How many days?",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q:\n%s", want, got)
		}
	}
	if !strings.HasSuffix(got, "Question: How many days?") {
		t.Errorf("question must come last, got tail %q", got[len(got)-30:])
	}
}

func TestBuildUserPromptWithNoPassages(t *testing.T) {
	got := answer.BuildUserPrompt("q", nil)
	if !strings.Contains(got, "<passages>\n</passages>") {
		t.Fatalf("empty passages block malformed:\n%s", got)
	}
}
