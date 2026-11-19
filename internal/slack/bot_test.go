package slack_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/Joshkaki00/4941-option-c/internal/answer"
	"github.com/Joshkaki00/4941-option-c/internal/slack"
	"github.com/Joshkaki00/4941-option-c/internal/store"
)

type fakeClient struct {
	mu      sync.Mutex
	posts   []string
	updates []string
	threads []string
	updated chan string
	postErr error
}

func newFakeClient() *fakeClient { return &fakeClient{updated: make(chan string, 16)} }

func (c *fakeClient) PostMessage(_ context.Context, _, threadTS, text string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.postErr != nil {
		return "", c.postErr
	}
	c.posts = append(c.posts, text)
	c.threads = append(c.threads, threadTS)
	return "ts-1", nil
}

func (c *fakeClient) UpdateMessage(_ context.Context, _, _, text string) error {
	c.mu.Lock()
	c.updates = append(c.updates, text)
	c.mu.Unlock()
	c.updated <- text
	return nil
}

func (c *fakeClient) snapshot() (posts, updates []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.posts...), append([]string(nil), c.updates...)
}

type fakeStore struct {
	mu       sync.Mutex
	messages []store.MessageRow
}

func (s *fakeStore) CreateConversation(_ context.Context, _ string) (string, error) {
	return "conv-1", nil
}

func (s *fakeStore) InsertMessage(_ context.Context, m store.MessageRow, _ []store.RetrievedRow) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, m)
	return "msg-1", nil
}

type fakeAsker struct {
	mu   sync.Mutex
	reqs []answer.Request
	res  answer.Result
	err  error
}

func (a *fakeAsker) Ask(_ context.Context, req answer.Request, _ func(string) error) (answer.Result, error) {
	a.mu.Lock()
	a.reqs = append(a.reqs, req)
	a.mu.Unlock()
	return a.res, a.err
}

func answerOK() answer.Result {
	return answer.Result{Outcome: answer.Answered, Answer: "15 days [1].",
		Citations: []answer.Citation{{Rank: 1, DocumentTitle: "PTO Policy"}}}
}

func testConfig() slack.Config {
	return slack.Config{BotUserID: botID, Workers: 2, QueueSize: 8, AskTimeout: 5 * time.Second}
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// startBot runs the worker pool until the test ends.
func startBot(t *testing.T, cfg slack.Config, c slack.Client, st slack.Store, a slack.Asker) *slack.Bot {
	t.Helper()
	bot := slack.NewBot(cfg, c, st, a, quiet)
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

func waitForUpdate(t *testing.T, c *fakeClient) string {
	t.Helper()
	select {
	case text := <-c.updated:
		return text
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the bot to update its reply")
		return ""
	}
}

func question(text string) slack.InnerEvent {
	return slack.InnerEvent{Type: "app_mention", User: "U1", Channel: "C1", TS: "1700.5", Text: "<@" + botID + "> " + text}
}

// --- worker behavior ---

func TestBotPostsPlaceholderThenUpdatesWithAnswer(t *testing.T) {
	c := newFakeClient()
	bot := startBot(t, testConfig(), c, &fakeStore{}, &fakeAsker{res: answerOK()})
	bot.HandleEvent(context.Background(), question("How many PTO days?"))

	final := waitForUpdate(t, c)
	if !strings.Contains(final, "15 days [1].") || !strings.Contains(final, "PTO Policy") {
		t.Fatalf("final text = %q", final)
	}
	posts, _ := c.snapshot()
	if len(posts) != 1 || posts[0] != slack.PlaceholderText {
		t.Fatalf("placeholder posts = %v", posts)
	}
}

func TestBotAsksWithCompanyWideAudienceOnly(t *testing.T) {
	c := newFakeClient()
	a := &fakeAsker{res: answer.Result{Outcome: answer.DeclinedNoSource, Answer: answer.DeclineText}}
	bot := startBot(t, testConfig(), c, &fakeStore{}, a)
	bot.HandleEvent(context.Background(), question("How many PTO days?"))
	waitForUpdate(t, c)

	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.reqs) != 1 {
		t.Fatalf("asks = %d", len(a.reqs))
	}
	if got := a.reqs[0].Audiences; len(got) != 1 || got[0] != "all-employees" {
		t.Fatalf("Slack audiences = %v; restricted docs need an explicit mapping", got)
	}
	if a.reqs[0].Question != "How many PTO days?" {
		t.Fatalf("question = %q", a.reqs[0].Question)
	}
}

func TestBotPersistsAnsweredMessageUnderSlackSubject(t *testing.T) {
	c := newFakeClient()
	st := &fakeStore{}
	bot := startBot(t, testConfig(), c, st, &fakeAsker{res: answerOK()})
	bot.HandleEvent(context.Background(), question("pto?"))
	waitForUpdate(t, c)

	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.messages) != 1 {
		t.Fatalf("persisted %d messages", len(st.messages))
	}
	m := st.messages[0]
	if m.UserSub != "slack:U1" || m.Outcome != "answered" || m.ConversationID != "conv-1" {
		t.Fatalf("message row = %+v", m)
	}
}

func TestBotAskFailureUpdatesWithFailureText(t *testing.T) {
	c := newFakeClient()
	bot := startBot(t, testConfig(), c, &fakeStore{}, &fakeAsker{err: errors.New("retrieval down")})
	bot.HandleEvent(context.Background(), question("pto?"))
	if final := waitForUpdate(t, c); !strings.Contains(final, "Sorry") {
		t.Fatalf("failure text = %q", final)
	}
}

func TestBotPlaceholderFailureSkipsWork(t *testing.T) {
	c := newFakeClient()
	c.postErr = errors.New("slack down")
	a := &fakeAsker{}
	bot := startBot(t, testConfig(), c, &fakeStore{}, a)
	bot.HandleEvent(context.Background(), question("pto?"))
	time.Sleep(100 * time.Millisecond)
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.reqs) != 0 {
		t.Fatalf("asked %d times although the placeholder failed", len(a.reqs))
	}
}

// --- backpressure ---

type blockingAsker struct{ release chan struct{} }

func (a *blockingAsker) Ask(ctx context.Context, _ answer.Request, _ func(string) error) (answer.Result, error) {
	select {
	case <-a.release:
	case <-ctx.Done():
	}
	return answer.Result{Outcome: answer.DeclinedNoSource, Answer: answer.DeclineText}, nil
}

func TestBotRepliesBusyWhenQueueIsFull(t *testing.T) {
	c := newFakeClient()
	cfg := testConfig()
	cfg.Workers = 1
	cfg.QueueSize = 1
	release := make(chan struct{})
	defer close(release)
	bot := startBot(t, cfg, c, &fakeStore{}, &blockingAsker{release: release})

	ctx := context.Background()
	bot.HandleEvent(ctx, question("one")) // occupies the worker
	time.Sleep(50 * time.Millisecond)
	bot.HandleEvent(ctx, question("two"))   // fills the queue
	bot.HandleEvent(ctx, question("three")) // must get the busy reply

	time.Sleep(100 * time.Millisecond)
	posts, _ := c.snapshot()
	busy := 0
	for _, p := range posts {
		if p == slack.BusyText {
			busy++
		}
	}
	if busy != 1 {
		t.Fatalf("busy replies = %d, want 1 (posts: %v)", busy, posts)
	}
}

// --- lifecycle ---

func TestRunReturnsAndLeavesNoGoroutinesAfterCancel(t *testing.T) {
	defer goleak.VerifyNone(t)
	bot := slack.NewBot(testConfig(), newFakeClient(), &fakeStore{}, &fakeAsker{}, quiet)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		bot.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
