package slack

import (
	"context"

	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// Listen reads Socket Mode events until ctx is cancelled or the channel closes.
// Each events_api envelope is acknowledged before anything else, so Slack never waits on
// answer generation. Hello and disconnect frames are skipped.
func (b *Bot) Listen(ctx context.Context, events <-chan socketmode.Event, ack func(socketmode.Request) error) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-events:
			if !ok {
				return
			}
			if evt.Type != socketmode.EventTypeEventsAPI || evt.Request == nil {
				continue
			}
			if err := ack(*evt.Request); err != nil {
				b.log.Error("slack ack failed", "envelope", evt.Request.EnvelopeID, "err", err)
			}
			data, ok := evt.Data.(slackevents.EventsAPIEvent)
			if !ok {
				continue
			}
			if inner, ok := toInner(data); ok {
				b.HandleEvent(ctx, inner)
			}
		}
	}
}

// toInner converts a library event into the bot's InnerEvent. The library delivers inner
// events as pointers (see slackevents.ParseEvent), so those are the only types handled here.
func toInner(data slackevents.EventsAPIEvent) (InnerEvent, bool) {
	if data.Type != slackevents.CallbackEvent {
		return InnerEvent{}, false
	}
	switch ev := data.InnerEvent.Data.(type) {
	case *slackevents.AppMentionEvent:
		return InnerEvent{
			Type: "app_mention", User: ev.User, Text: ev.Text, TS: ev.TimeStamp,
			ThreadTS: ev.ThreadTimeStamp, Channel: ev.Channel, BotID: ev.BotID,
		}, true
	case *slackevents.MessageEvent:
		return InnerEvent{
			Type: "message", ChannelType: ev.ChannelType, User: ev.User, Text: ev.Text, TS: ev.TimeStamp,
			ThreadTS: ev.ThreadTimeStamp, Channel: ev.Channel, BotID: ev.BotID, Subtype: ev.SubType,
		}, true
	}
	return InnerEvent{}, false
}
