package slack

import "strings"

// Envelope is the outer Slack event payload.
type Envelope struct {
	Type      string      `json:"type"`
	Challenge string      `json:"challenge"`
	Event     *InnerEvent `json:"event"`
}

// InnerEvent is the event itself (app_mention or message).
type InnerEvent struct {
	Type        string `json:"type"`
	ChannelType string `json:"channel_type"`
	User        string `json:"user"`
	Text        string `json:"text"`
	TS          string `json:"ts"`
	ThreadTS    string `json:"thread_ts"`
	Channel     string `json:"channel"`
	BotID       string `json:"bot_id"`
	Subtype     string `json:"subtype"`
}

// Question returns the text to answer for a relevant event: a mention of the bot,
// or a direct message. Bot messages, edits, and other channel chatter are ignored.
func Question(ev InnerEvent, botUserID string) (string, bool) {
	if ev.BotID != "" || ev.Subtype != "" {
		return "", false
	}
	var text string
	switch {
	case ev.Type == "app_mention":
		text = strings.ReplaceAll(ev.Text, "<@"+botUserID+">", "")
	case ev.Type == "message" && ev.ChannelType == "im":
		text = ev.Text
	default:
		return "", false
	}
	q := strings.TrimSpace(text)
	return q, q != ""
}
