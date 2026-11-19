package slack_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/Joshkaki00/4941-option-c/internal/slack"
)

// listen runs Listen in a goroutine and returns a stop function that waits for it to return.
func listen(t *testing.T, b *slack.Bot, events <-chan socketmode.Event, ack func(socketmode.Request) error) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		b.Listen(ctx, events, ack)
		close(done)
	}()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Listen did not return after cancel")
		}
	}
}

func eventsAPI(envelope, innerType string, inner any) socketmode.Event {
	return socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			Type:       slackevents.CallbackEvent,
			InnerEvent: slackevents.EventsAPIInnerEvent{Type: innerType, Data: inner},
		},
		Request: &socketmode.Request{Type: socketmode.RequestTypeEventsAPI, EnvelopeID: envelope},
	}
}

func mention(ts, thread, text string) socketmode.Event {
	return eventsAPI("env-1", "app_mention", &slackevents.AppMentionEvent{
		Type: "app_mention", User: "U1", Channel: "C1", TimeStamp: ts, ThreadTimeStamp: thread, Text: text,
	})
}

func TestListenAcknowledgesBeforeAnswering(t *testing.T) {
	c := newFakeClient()
	bot := slack.NewBot(testConfig(), c, &fakeStore{}, &fakeAsker{}, quiet)

	events := make(chan socketmode.Event, 1)
	var sawPostsAtAck int
	acked := make(chan string, 1)
	stop := listen(t, bot, events, func(req socketmode.Request) error {
		c.mu.Lock()
		sawPostsAtAck = len(c.posts)
		c.mu.Unlock()
		acked <- req.EnvelopeID
		return nil
	})
	defer stop()

	events <- mention("1700.5", "", "<@UBOT> pto?")
	select {
	case id := <-acked:
		if id != "env-1" {
			t.Fatalf("acked envelope %q", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("event was not acknowledged")
	}
	if sawPostsAtAck != 0 {
		t.Fatalf("answer work started before the ack (%d posts)", sawPostsAtAck)
	}
}

func TestListenStillDispatchesWhenAckFails(t *testing.T) {
	c := newFakeClient()
	a := &fakeAsker{res: answerOK()}
	bot := slack.NewBot(testConfig(), c, &fakeStore{}, a, quiet)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bot.Run(ctx)

	events := make(chan socketmode.Event, 1)
	stop := listen(t, bot, events, func(socketmode.Request) error { return errors.New("socket closed") })
	defer stop()

	events <- mention("1700.5", "", "<@UBOT> pto?")
	waitForUpdate(t, c)
}

func TestListenAnswersDirectMessages(t *testing.T) {
	c := newFakeClient()
	bot := slack.NewBot(testConfig(), c, &fakeStore{}, &fakeAsker{res: answerOK()}, quiet)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bot.Run(ctx)

	events := make(chan socketmode.Event, 1)
	stop := listen(t, bot, events, func(socketmode.Request) error { return nil })
	defer stop()

	events <- eventsAPI("env-2", "message", &slackevents.MessageEvent{
		Type: "message", ChannelType: "im", User: "U1", Channel: "D1", TimeStamp: "1.0", Text: "sick leave?",
	})
	waitForUpdate(t, c)
	posts, _ := c.snapshot()
	if len(posts) != 1 {
		t.Fatalf("DM posts = %v", posts)
	}
}

func TestListenIgnoresBotAndChannelMessages(t *testing.T) {
	c := newFakeClient()
	bot := slack.NewBot(testConfig(), c, &fakeStore{}, &fakeAsker{}, quiet)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bot.Run(ctx)

	events := make(chan socketmode.Event, 4)
	var acks int
	var mu sync.Mutex
	stop := listen(t, bot, events, func(socketmode.Request) error { mu.Lock(); acks++; mu.Unlock(); return nil })
	defer stop()

	events <- eventsAPI("e-b", "message", &slackevents.MessageEvent{Type: "message", ChannelType: "im", BotID: "B9", Text: "x", Channel: "D1", TimeStamp: "1"})
	events <- eventsAPI("e-c", "message", &slackevents.MessageEvent{Type: "message", ChannelType: "channel", Text: "pto?", Channel: "C1", TimeStamp: "2"})
	// Hello and disconnect carry no events_api payload: they must be skipped quietly.
	events <- socketmode.Event{Type: socketmode.EventTypeHello}
	events <- socketmode.Event{Type: socketmode.EventTypeDisconnect}

	time.Sleep(150 * time.Millisecond)
	posts, _ := c.snapshot()
	if len(posts) != 0 {
		t.Fatalf("ignored events produced posts: %v", posts)
	}
	mu.Lock()
	defer mu.Unlock()
	if acks != 2 {
		t.Fatalf("acks = %d, want 2 (both events_api envelopes are acknowledged)", acks)
	}
}

func TestListenUsesThreadRootForReplies(t *testing.T) {
	c := newFakeClient()
	bot := slack.NewBot(testConfig(), c, &fakeStore{}, &fakeAsker{res: answerOK()}, quiet)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bot.Run(ctx)

	events := make(chan socketmode.Event, 2)
	stop := listen(t, bot, events, func(socketmode.Request) error { return nil })
	defer stop()

	events <- mention("1700.9", "1700.2", "<@UBOT> pto?")
	waitForUpdate(t, c)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.threads) != 1 || c.threads[0] != "1700.2" {
		t.Fatalf("placeholder thread = %v, want the parent thread 1700.2", c.threads)
	}
}

func TestListenReturnsWhenContextCancelled(t *testing.T) {
	bot := slack.NewBot(testConfig(), newFakeClient(), &fakeStore{}, &fakeAsker{}, quiet)
	events := make(chan socketmode.Event)
	stop := listen(t, bot, events, func(socketmode.Request) error { return nil })
	stop() // fails the test if Listen does not return
}
