package slack_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	updated chan string
	postErr error
	threads []string
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

func (c *fakeClient) UpdateMessage(_ context.Context, _, ts, text string) error {
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

func testConfig() slack.Config {
	return slack.Config{
		SigningSecret: signingSecret,
		BotUserID:     botID,
		Workers:       2,
		QueueSize:     8,
		AskTimeout:    5 * time.Second,
	}
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// startBot runs the bot until the test ends and returns its handler.
func startBot(t *testing.T, cfg slack.Config, c slack.Client, st slack.Store, a slack.Asker) (*slack.Bot, http.Handler) {
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
	return bot, bot.Handler()
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

func postEvent(t *testing.T, h http.Handler, body string, retry bool) int {
	t.Helper()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req := httptest.NewRequest(http.MethodPost, "/slack/events", strings.NewReader(body))
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", slackSig(signingSecret, ts, []byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if retry {
		req.Header.Set("X-Slack-Retry-Num", "1")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

const mentionEvent = `{"type":"event_callback","event":{"type":"app_mention","user":"U1","channel":"C1","ts":"1700.5","text":"<@UBOT> How many PTO days?"}}`

// --- worker behavior ---

func TestBotPostsPlaceholderThenUpdatesWithAnswer(t *testing.T) {
	c := newFakeClient()
	a := &fakeAsker{res: answer.Result{Outcome: answer.Answered, Answer: "15 days [1].",
		Citations: []answer.Citation{{Rank: 1, DocumentTitle: "PTO Policy"}}}}
	_, h := startBot(t, testConfig(), c, &fakeStore{}, a)

	if code := postEvent(t, h, mentionEvent, false); code != http.StatusOK {
		t.Fatalf("ack status %d, want 200", code)
	}
	final := waitForUpdate(t, c)
	if !strings.Contains(final, "15 days [1].") || !strings.Contains(final, "PTO Policy") {
		t.Fatalf("final text = %q", final)
	}
	posts, _ := c.snapshot()
	if len(posts) != 1 || !strings.Contains(posts[0], "Looking that up") {
		t.Fatalf("placeholder posts = %v", posts)
	}
}

func TestBotRepliesInThreadWhenMentionedInThread(t *testing.T) {
	c := newFakeClient()
	a := &fakeAsker{res: answer.Result{Outcome: answer.DeclinedNoSource, Answer: answer.DeclineText}}
	_, h := startBot(t, testConfig(), c, &fakeStore{}, a)

	body := `{"type":"event_callback","event":{"type":"app_mention","user":"U1","channel":"C1","ts":"1700.9","thread_ts":"1700.2","text":"<@UBOT> espresso?"}}`
	postEvent(t, h, body, false)
	waitForUpdate(t, c)
	_, threads := func() (int, []string) {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.threads), append([]string(nil), c.threads...)
	}()
	if len(threads) != 1 || threads[0] != "1700.2" {
		t.Fatalf("placeholder thread = %v, want the parent thread_ts", threads)
	}
}

func TestBotAsksWithCompanyWideAudienceOnly(t *testing.T) {
	c := newFakeClient()
	a := &fakeAsker{res: answer.Result{Outcome: answer.DeclinedNoSource, Answer: answer.DeclineText}}
	_, h := startBot(t, testConfig(), c, &fakeStore{}, a)
	postEvent(t, h, mentionEvent, false)
	waitForUpdate(t, c)

	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.reqs) != 1 {
		t.Fatalf("asks = %d", len(a.reqs))
	}
	if got := a.reqs[0].Audiences; len(got) != 1 || got[0] != "all-employees" {
		t.Fatalf("Slack audiences = %v; restricted docs must need an explicit mapping", got)
	}
	if a.reqs[0].Question != "How many PTO days?" {
		t.Fatalf("question = %q", a.reqs[0].Question)
	}
}

func TestBotPersistsAnsweredMessageUnderSlackSubject(t *testing.T) {
	c := newFakeClient()
	st := &fakeStore{}
	a := &fakeAsker{res: answer.Result{Outcome: answer.Answered, Answer: "x [1].",
		Citations: []answer.Citation{{Rank: 1, DocumentTitle: "PTO Policy"}}}}
	_, h := startBot(t, testConfig(), c, st, a)
	postEvent(t, h, mentionEvent, false)
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
	a := &fakeAsker{err: errors.New("retrieval down")}
	_, h := startBot(t, testConfig(), c, &fakeStore{}, a)
	postEvent(t, h, mentionEvent, false)
	final := waitForUpdate(t, c)
	if !strings.Contains(final, "Sorry") {
		t.Fatalf("failure text = %q", final)
	}
}

func TestBotPlaceholderFailureSkipsWork(t *testing.T) {
	c := newFakeClient()
	c.postErr = errors.New("slack down")
	a := &fakeAsker{}
	_, h := startBot(t, testConfig(), c, &fakeStore{}, a)
	postEvent(t, h, mentionEvent, false)
	// Give the worker a moment; no ask should happen without a placeholder to update.
	time.Sleep(100 * time.Millisecond)
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.reqs) != 0 {
		t.Fatalf("asked %d times although the placeholder failed", len(a.reqs))
	}
}

// --- handler: verification, routing, backpressure ---

func TestHandlerRejectsBadSignature(t *testing.T) {
	_, h := startBot(t, testConfig(), newFakeClient(), &fakeStore{}, &fakeAsker{})
	req := httptest.NewRequest(http.MethodPost, "/slack/events", strings.NewReader(mentionEvent))
	req.Header.Set("X-Slack-Request-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	req.Header.Set("X-Slack-Signature", "v0=00")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
}

func TestHandlerAnswersURLVerificationChallenge(t *testing.T) {
	_, h := startBot(t, testConfig(), newFakeClient(), &fakeStore{}, &fakeAsker{})
	body := `{"type":"url_verification","challenge":"abc123"}`
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req := httptest.NewRequest(http.MethodPost, "/slack/events", strings.NewReader(body))
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", slackSig(signingSecret, ts, []byte(body)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusOK || out["challenge"] != "abc123" {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerIgnoresRetriesAndBotMessages(t *testing.T) {
	c := newFakeClient()
	a := &fakeAsker{}
	_, h := startBot(t, testConfig(), c, &fakeStore{}, a)

	postEvent(t, h, mentionEvent, true) // Slack retry: must not answer twice
	botMsg := `{"type":"event_callback","event":{"type":"message","channel_type":"im","user":"U1","channel":"D1","ts":"1","bot_id":"B9","text":"hi"}}`
	postEvent(t, h, botMsg, false)

	time.Sleep(100 * time.Millisecond)
	posts, _ := c.snapshot()
	if len(posts) != 0 {
		t.Fatalf("retries and bot messages must be ignored, got posts %v", posts)
	}
}

func TestHandlerRepliesBusyWhenQueueIsFull(t *testing.T) {
	c := newFakeClient()
	cfg := testConfig()
	cfg.Workers = 1
	cfg.QueueSize = 1
	block := make(chan struct{})
	a := &blockingAsker{release: block}
	_, h := startBot(t, cfg, c, &fakeStore{}, a)
	defer close(block)

	// First question occupies the worker, second fills the queue, third must get "busy".
	for i := 0; i < 2; i++ {
		postEvent(t, h, mentionEvent, false)
	}
	time.Sleep(100 * time.Millisecond)
	postEvent(t, h, mentionEvent, false)
	time.Sleep(100 * time.Millisecond)

	posts, _ := c.snapshot()
	busy := 0
	for _, p := range posts {
		if strings.Contains(p, "a lot of questions") {
			busy++
		}
	}
	if busy != 1 {
		t.Fatalf("busy replies = %d, want 1 (posts: %v)", busy, posts)
	}
}

type blockingAsker struct{ release chan struct{} }

func (a *blockingAsker) Ask(ctx context.Context, _ answer.Request, _ func(string) error) (answer.Result, error) {
	select {
	case <-a.release:
	case <-ctx.Done():
	}
	return answer.Result{Outcome: answer.DeclinedNoSource, Answer: answer.DeclineText}, nil
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
