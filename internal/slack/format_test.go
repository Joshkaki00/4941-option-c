package slack_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Joshkaki00/4941-option-c/internal/answer"
	"github.com/Joshkaki00/4941-option-c/internal/slack"
)

func TestFormatAnsweredListsCitedSources(t *testing.T) {
	res := answer.Result{
		Outcome: answer.Answered,
		Answer:  "Full-time employees get 15 days [1].",
		Citations: []answer.Citation{{
			Rank: 1, DocumentTitle: "PTO Policy", Section: "Accrual",
			LastUpdated: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		}},
	}
	text := slack.FormatAnswer(res)
	for _, want := range []string{"Full-time employees get 15 days [1].", "Sources:", "PTO Policy › Accrual", "updated 2026-03-01"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}

func TestFormatOmitsSectionSeparatorWhenNoSection(t *testing.T) {
	res := answer.Result{
		Outcome:   answer.Answered,
		Answer:    "x [1].",
		Citations: []answer.Citation{{Rank: 1, DocumentTitle: "IT Password Reset"}},
	}
	text := slack.FormatAnswer(res)
	if strings.Contains(text, "›") {
		t.Fatalf("no section, so no separator:\n%s", text)
	}
}

func TestFormatDeclineIncludesHandoff(t *testing.T) {
	res := answer.Result{
		Outcome: answer.DeclinedNoSource,
		Answer:  answer.DeclineText,
		Handoff: "Try HR.",
	}
	text := slack.FormatAnswer(res)
	if !strings.Contains(text, "I don't know") || !strings.Contains(text, "Try HR.") {
		t.Fatalf("decline text:\n%s", text)
	}
	if strings.Contains(text, "Sources:") {
		t.Fatal("a decline must not list sources")
	}
}

func TestFormatRoutedToHumanIncludesHandoff(t *testing.T) {
	res := answer.Result{Outcome: answer.RoutedToHuman, Answer: answer.SensitiveText, Handoff: "Contact People Ops."}
	if text := slack.FormatAnswer(res); !strings.Contains(text, "Contact People Ops.") {
		t.Fatalf("handoff missing:\n%s", text)
	}
}

func TestFormatAnsweredWithoutHandoffTextHasNoTrailingBlock(t *testing.T) {
	res := answer.Result{Outcome: answer.DeclinedNoSource, Answer: answer.DeclineText}
	if text := slack.FormatAnswer(res); strings.HasSuffix(text, "\n\n") {
		t.Fatalf("trailing blank block: %q", text)
	}
}
