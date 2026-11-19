package slack_test

import (
	"testing"

	"github.com/Joshkaki00/4941-option-c/internal/slack"
)

const botID = "UBOT"

func TestQuestionFromAppMentionStripsBotMention(t *testing.T) {
	ev := slack.InnerEvent{Type: "app_mention", Text: "<@UBOT>   How many PTO days?  "}
	q, ok := slack.Question(ev, botID)
	if !ok || q != "How many PTO days?" {
		t.Fatalf("question = %q, ok = %v", q, ok)
	}
}

func TestQuestionFromDirectMessage(t *testing.T) {
	ev := slack.InnerEvent{Type: "message", ChannelType: "im", Text: "sick leave?"}
	q, ok := slack.Question(ev, botID)
	if !ok || q != "sick leave?" {
		t.Fatalf("dm question = %q, ok = %v", q, ok)
	}
}

func TestQuestionIgnoresBotsEditsAndChannelChatter(t *testing.T) {
	cases := map[string]slack.InnerEvent{
		"bot message":     {Type: "message", ChannelType: "im", Text: "hi", BotID: "B1"},
		"subtype edit":    {Type: "message", ChannelType: "im", Text: "hi", Subtype: "message_changed"},
		"channel message": {Type: "message", ChannelType: "channel", Text: "hi"},
		"empty mention":   {Type: "app_mention", Text: "<@UBOT>   "},
		"empty dm":        {Type: "message", ChannelType: "im", Text: "   "},
		"unrelated event": {Type: "reaction_added", Text: "x"},
	}
	for name, ev := range cases {
		if q, ok := slack.Question(ev, botID); ok {
			t.Errorf("%s: produced question %q", name, q)
		}
	}
}

func TestQuestionLeavesOtherMentionsAlone(t *testing.T) {
	ev := slack.InnerEvent{Type: "app_mention", Text: "<@UBOT> ask <@UOTHER> about PTO"}
	q, ok := slack.Question(ev, botID)
	if !ok || q != "ask <@UOTHER> about PTO" {
		t.Fatalf("question = %q", q)
	}
}
