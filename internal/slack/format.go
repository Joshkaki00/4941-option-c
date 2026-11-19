package slack

import (
	"strings"

	"github.com/Joshkaki00/4941-option-c/internal/answer"
)

// Copy shown in Slack. Kept here so tests and the worker agree on wording.
const (
	PlaceholderText = "Looking that up in the handbook…"
	FailureText     = "Sorry, I couldn't answer that just now. Please try again in a moment."
	BusyText        = "I'm handling a lot of questions right now. Please ask again in a minute."
)

// FormatAnswer renders an answer as Slack text. Sources are listed only when the
// answer is grounded (answered outcome); declines show the handoff instead.
func FormatAnswer(res answer.Result) string {
	var b strings.Builder
	b.WriteString(res.Answer)
	if res.Outcome == answer.Answered && len(res.Citations) > 0 {
		b.WriteString("\n\nSources:")
		for _, c := range res.Citations {
			b.WriteString("\n• " + c.DocumentTitle)
			if c.Section != "" {
				b.WriteString(" › " + c.Section)
			}
			if !c.LastUpdated.IsZero() {
				b.WriteString(" (updated " + c.LastUpdated.Format("2006-01-02") + ")")
			}
		}
	}
	if res.Outcome != answer.Answered && res.Handoff != "" {
		b.WriteString("\n\n" + res.Handoff)
	}
	return b.String()
}
