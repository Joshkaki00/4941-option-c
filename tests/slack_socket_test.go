package tests

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/Joshkaki00/4941-option-c/internal/answer"
	"github.com/Joshkaki00/4941-option-c/internal/slack"
	"github.com/Joshkaki00/4941-option-c/internal/store"
)

// Black-box tests of the Socket Mode entry point: Listen acknowledges envelopes and hands
// questions to the bot, which posts a placeholder and then the answer.

type sockClient struct {
	mu      sync.Mutex
	posts   []string
	updates chan string
}

func (c *sockClient) PostMessage(_ context.Context, _, _, text string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.posts = append(c.posts, text)
	return "ts-1", nil
}

func (c *sockClient) UpdateMessage(_ context.Context, _, _, text string) error {
	c.updates <- text
	return nil
}

func (c *sockClient) postCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.posts)
}

type sockStore struct{}

func (sockStore) CreateConversation(context.Context, string) (string, error) { return "c", nil }
func (sockStore) InsertMessage(context.Context, store.MessageRow, []store.RetrievedRow) (string, error) {
	return "m", nil
}

type sockAsker struct{}

func (sockAsker) Ask(context.Context, answer.Request, func(string) error) (answer.Result, error) {
	return answer.Result{Outcome: answer.Answered, Answer: "15 days [1]."}, nil
}

func sockBot(t *testing.T, c *sockClient) *slack.Bot {
	t.Helper()
	bot := slack.NewBot(slack.Config{BotUserID: "UBOT", Workers: 1, QueueSize: 4, AskTimeout: 5 * time.Second},
		c, sockStore{}, sockAsker{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		bot.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return bot
}

// runListen starts Listen and returns a stop function that waits for it to return.
func runListen(t *testing.T, bot *slack.Bot, events <-chan socketmode.Event, ack func(socketmode.Request) error) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		bot.Listen(ctx, events, ack)
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

func appMentionEnvelope(envelope, text string) socketmode.Event {
	return socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent,
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: "app_mention",
				Data: &slackevents.AppMentionEvent{Type: "app_mention", User: "U1", Channel: "C1", TimeStamp: "1700.5", Text: text},
			},
		},
		Request: &socketmode.Request{Type: socketmode.RequestTypeEventsAPI, EnvelopeID: envelope},
	}
}

func TestSocketMentionIsAnsweredInThread(t *testing.T) {
	c := &sockClient{updates: make(chan string, 4)}
	bot := sockBot(t, c)
	events := make(chan socketmode.Event, 1)
	stop := runListen(t, bot, events, func(socketmode.Request) error { return nil })
	defer stop()

	events <- appMentionEnvelope("env-1", "<@UBOT> How many vacation days?")
	select {
	case final := <-c.updates:
		if !strings.Contains(final, "15 days") {
			t.Fatalf("final = %q", final)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no answer delivered")
	}
}

func TestSocketAcknowledgesBeforePlaceholder(t *testing.T) {
	c := &sockClient{updates: make(chan string, 4)}
	bot := sockBot(t, c)
	events := make(chan socketmode.Event, 1)
	var seenPosts int
	acked := make(chan struct{})
	stop := runListen(t, bot, events, func(socketmode.Request) error {
		seenPosts = c.postCount()
		close(acked)
		return nil
	})
	defer stop()

	events <- appMentionEnvelope("env-2", "<@UBOT> pto?")
	<-acked
	if seenPosts != 0 {
		t.Fatalf("answer started before the ack (%d posts)", seenPosts)
	}
}

func TestSocketIgnoresHelloAndDisconnect(t *testing.T) {
	c := &sockClient{updates: make(chan string, 4)}
	bot := sockBot(t, c)
	events := make(chan socketmode.Event, 2)
	acks := make(chan string, 4)
	stop := runListen(t, bot, events, func(r socketmode.Request) error { acks <- r.EnvelopeID; return nil })
	defer stop()

	events <- socketmode.Event{Type: socketmode.EventTypeHello}
	events <- socketmode.Event{Type: socketmode.EventTypeDisconnect}
	time.Sleep(100 * time.Millisecond)
	if n := len(acks); n != 0 {
		t.Fatalf("hello/disconnect were acknowledged %d times", n)
	}
	if c.postCount() != 0 {
		t.Fatal("control frames produced replies")
	}
}

func TestSocketAcknowledgesButDoesNotAnswerBotMessages(t *testing.T) {
	c := &sockClient{updates: make(chan string, 4)}
	bot := sockBot(t, c)
	events := make(chan socketmode.Event, 1)
	acks := make(chan string, 2)
	stop := runListen(t, bot, events, func(r socketmode.Request) error { acks <- r.EnvelopeID; return nil })
	defer stop()

	events <- socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent,
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: "message",
				Data: &slackevents.MessageEvent{Type: "message", ChannelType: "im", BotID: "B9", Text: "x", Channel: "D1", TimeStamp: "1"},
			},
		},
		Request: &socketmode.Request{Type: socketmode.RequestTypeEventsAPI, EnvelopeID: "env-bot"},
	}
	select {
	case id := <-acks:
		if id != "env-bot" {
			t.Fatalf("acked %q", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("envelope not acknowledged")
	}
	time.Sleep(100 * time.Millisecond)
	if c.postCount() != 0 {
		t.Fatal("bot's own message was answered")
	}
}
