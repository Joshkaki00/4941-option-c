package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Joshkaki00/4941-option-c/internal/answer"
	"github.com/Joshkaki00/4941-option-c/internal/auth"
	"github.com/Joshkaki00/4941-option-c/internal/retrieval"
	"github.com/Joshkaki00/4941-option-c/internal/store"
)

// ---- in-memory fakes -------------------------------------------------------

type memStore struct {
	mu        sync.Mutex
	convs     map[string]string // id -> owner sub
	messages  map[string]store.MessageRow
	retrieved map[string][]store.RetrievedRow
	feedback  []string
	nextID    int
}

func newMemStore() *memStore {
	return &memStore{convs: map[string]string{}, messages: map[string]store.MessageRow{}, retrieved: map[string][]store.RetrievedRow{}}
}

// newUUID must be called with m.mu held.
func (m *memStore) newUUID() string {
	m.nextID++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", m.nextID)
}

func (m *memStore) CreateConversation(_ context.Context, sub string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.newUUID()
	m.convs[id] = sub
	return id, nil
}

func (m *memStore) ConversationOwnedBy(_ context.Context, id, sub string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.convs[id] == sub && sub != "", nil
}

func (m *memStore) InsertMessage(_ context.Context, row store.MessageRow, ret []store.RetrievedRow) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.newUUID()
	m.messages[id] = row
	m.retrieved[id] = ret
	return id, nil
}

func (m *memStore) MessageOwner(_ context.Context, id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.messages[id]
	if !ok {
		return "", store.ErrNotFound
	}
	return row.UserSub, nil
}

func (m *memStore) InsertFeedback(_ context.Context, msgID, sub string, rating int, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.feedback = append(m.feedback, msgID+":"+sub)
	return nil
}

type fakeChunks struct {
	view  retrieval.ChunkView
	found bool
}

func (f fakeChunks) ChunkForUser(_ context.Context, _ []string, _ string) (retrieval.ChunkView, bool, error) {
	return f.view, f.found, nil
}

type fakeAsker struct {
	tokens []string
	result answer.Result
	err    error
	block  chan struct{}
}

func (f *fakeAsker) Ask(ctx context.Context, _ answer.Request, onToken func(string) error) (answer.Result, error) {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return answer.Result{}, ctx.Err()
		}
	}
	for _, tok := range f.tokens {
		if err := onToken(tok); err != nil {
			return answer.Result{}, err
		}
	}
	return f.result, f.err
}

// ---- helpers ---------------------------------------------------------------

const (
	aliceTok = "alice-token"
	bobTok   = "bob-token"
)

func verifier() auth.Verifier {
	return auth.StaticTokens{
		aliceTok: {Subject: "alice", Audiences: []string{"all-employees"}},
		bobTok:   {Subject: "bob", Audiences: []string{"all-employees"}},
	}
}

func newTestServer(t *testing.T, st *memStore, asker Asker, chunks ChunkReader) http.Handler {
	t.Helper()
	return New(Deps{
		Verifier:       verifier(),
		Store:          st,
		Chunks:         chunks,
		Asker:          asker,
		MaxConcurrent:  4,
		AcquireTimeout: 50 * time.Millisecond,
	}).Handler()
}

func do(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func createConv(t *testing.T, h http.Handler, token string) string {
	t.Helper()
	rec := do(h, http.MethodPost, "/v1/conversations", token, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("create conversation: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out.ID
}

func okResult() answer.Result {
	return answer.Result{
		Outcome: answer.Answered,
		Answer:  "Full-time employees receive 15 days [1].",
		Citations: []answer.Citation{{
			Rank: 1, ChunkID: "c1", VersionID: 1, DocumentTitle: "PTO Policy",
			Section: "Allotment", LastUpdated: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), Used: true,
		}},
		Retrieved: []answer.Citation{{Rank: 1, ChunkID: "c1", VersionID: 1, Used: true}},
	}
}

// ---- tests -----------------------------------------------------------------

func TestEveryVersionedRouteRequiresAuth(t *testing.T) {
	h := newTestServer(t, newMemStore(), &fakeAsker{}, fakeChunks{})
	routes := []struct{ method, path string }{
		{http.MethodPost, "/v1/conversations"},
		{http.MethodPost, "/v1/conversations/x/messages"},
		{http.MethodGet, "/v1/chunks/x"},
		{http.MethodPost, "/v1/messages/x/feedback"},
	}
	for _, r := range routes {
		if rec := do(h, r.method, r.path, "", "{}"); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without token: want 401, got %d", r.method, r.path, rec.Code)
		}
		if rec := do(h, r.method, r.path, "forged", "{}"); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s with bad token: want 401, got %d", r.method, r.path, rec.Code)
		}
	}
}

func TestHealthzIsPublic(t *testing.T) {
	h := newTestServer(t, newMemStore(), &fakeAsker{}, fakeChunks{})
	if rec := do(h, http.MethodGet, "/healthz", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

func TestAskNonStreamingReturnsCitedAnswerAndPersists(t *testing.T) {
	st := newMemStore()
	h := newTestServer(t, st, &fakeAsker{result: okResult()}, fakeChunks{})
	conv := createConv(t, h, aliceTok)

	rec := do(h, http.MethodPost, "/v1/conversations/"+conv+"/messages", aliceTok,
		`{"content":"How many vacation days do full-time employees get?","stream":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d %s", rec.Code, rec.Body)
	}
	var out struct {
		MessageID string `json:"message_id"`
		Outcome   string `json:"outcome"`
		Citations []struct {
			DocumentTitle string `json:"document_title"`
			Section       string `json:"section"`
			LastUpdated   string `json:"last_updated"`
		} `json:"citations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Outcome != "answered" || out.MessageID == "" {
		t.Fatalf("bad response: %+v", out)
	}
	if len(out.Citations) != 1 || out.Citations[0].DocumentTitle != "PTO Policy" ||
		out.Citations[0].Section != "Allotment" || out.Citations[0].LastUpdated != "2026-03-01" {
		t.Fatalf("citation missing title/section/last-updated: %+v", out.Citations)
	}
	if st.messages[out.MessageID].UserSub != "alice" {
		t.Fatal("message must be owned by the token identity")
	}
}

func TestAskIgnoresClientSuppliedUserID(t *testing.T) {
	st := newMemStore()
	h := newTestServer(t, st, &fakeAsker{result: okResult()}, fakeChunks{})
	conv := createConv(t, h, aliceTok)

	rec := do(h, http.MethodPost, "/v1/conversations/"+conv+"/messages", aliceTok,
		`{"content":"vacation","stream":false,"user_id":"admin","user_sub":"admin"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	for _, row := range st.messages {
		if row.UserSub != "alice" {
			t.Fatalf("body-supplied identity leaked into storage: %q", row.UserSub)
		}
	}
}

func TestAskOtherUsersConversationIsNotFound(t *testing.T) {
	h := newTestServer(t, newMemStore(), &fakeAsker{result: okResult()}, fakeChunks{})
	conv := createConv(t, h, aliceTok)

	rec := do(h, http.MethodPost, "/v1/conversations/"+conv+"/messages", bobTok,
		`{"content":"vacation","stream":false}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bob must not ask in alice's conversation: want 404, got %d", rec.Code)
	}
}

func TestAskRejectsEmptyAndOversizedContent(t *testing.T) {
	h := newTestServer(t, newMemStore(), &fakeAsker{result: okResult()}, fakeChunks{})
	conv := createConv(t, h, aliceTok)
	path := "/v1/conversations/" + conv + "/messages"

	if rec := do(h, http.MethodPost, path, aliceTok, `{"content":"   "}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty content: want 400, got %d", rec.Code)
	}
	long := strings.Repeat("x", maxQuestionRunes+1)
	if rec := do(h, http.MethodPost, path, aliceTok, `{"content":"`+long+`"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized content: want 400, got %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, path, aliceTok, `not json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: want 400, got %d", rec.Code)
	}
}

func TestAskStreamingEmitsTokensCitationsThenDone(t *testing.T) {
	st := newMemStore()
	h := newTestServer(t, st, &fakeAsker{tokens: []string{"Full-time ", "employees ", "get 15 days [1]."}, result: okResult()}, fakeChunks{})
	conv := createConv(t, h, aliceTok)

	rec := do(h, http.MethodPost, "/v1/conversations/"+conv+"/messages", aliceTok,
		`{"content":"vacation days","stream":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("want text/event-stream, got %q", ct)
	}
	events := parseSSE(t, rec.Body.String())
	var names []string
	for _, e := range events {
		names = append(names, e.name)
	}
	want := []string{"token", "token", "token", "citations", "done"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("event order: got %v want %v", names, want)
	}
	last := events[len(events)-1].data
	var done struct {
		MessageID string `json:"message_id"`
		Outcome   string `json:"outcome"`
	}
	_ = json.Unmarshal([]byte(last), &done)
	if done.MessageID == "" || done.Outcome != "answered" {
		t.Fatalf("done must carry message_id and outcome: %s", last)
	}
	if _, ok := st.messages[done.MessageID]; !ok {
		t.Fatal("message must be persisted before done is sent")
	}
}

func TestAskStreamingErrorBeforeOutputIs503(t *testing.T) {
	h := newTestServer(t, newMemStore(), &fakeAsker{err: errors.New("upstream down")}, fakeChunks{})
	conv := createConv(t, h, aliceTok)
	rec := do(h, http.MethodPost, "/v1/conversations/"+conv+"/messages", aliceTok,
		`{"content":"vacation","stream":true}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "upstream down") {
		t.Fatal("internal error text must not reach the client")
	}
}

func TestAskBusyReturns429WhenConcurrencyCapHeld(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	srv := New(Deps{
		Verifier: verifier(), Store: newMemStore(), Chunks: fakeChunks{},
		Asker: &fakeAsker{result: okResult(), block: block}, MaxConcurrent: 1, AcquireTimeout: 20 * time.Millisecond,
	}).Handler()

	conv := createConv(t, srv, aliceTok)
	path := "/v1/conversations/" + conv + "/messages"

	done := make(chan struct{})
	go func() {
		defer close(done)
		do(srv, http.MethodPost, path, aliceTok, `{"content":"first","stream":false}`)
	}()
	time.Sleep(30 * time.Millisecond) // let the first request take the only slot

	rec := do(srv, http.MethodPost, path, aliceTok, `{"content":"second","stream":false}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429 when cap is held, got %d", rec.Code)
	}
	block <- struct{}{}
	<-done
}

func TestChunkPreviewForbiddenDoesNotLeakContent(t *testing.T) {
	chunks := fakeChunks{found: true, view: retrieval.ChunkView{Allowed: false, Candidate: retrieval.Candidate{Content: "SECRET SALARY"}}}
	h := newTestServer(t, newMemStore(), &fakeAsker{}, chunks)

	rec := do(h, http.MethodGet, "/v1/chunks/c1", aliceTok, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "SECRET") {
		t.Fatal("forbidden response leaked content")
	}
}

func TestChunkPreviewAllowedReturnsContent(t *testing.T) {
	chunks := fakeChunks{found: true, view: retrieval.ChunkView{Allowed: true, Candidate: retrieval.Candidate{
		Title: "PTO Policy", HeadingPath: []string{"Allotment"}, Content: "15 days", LastUpdated: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	}}}
	h := newTestServer(t, newMemStore(), &fakeAsker{}, chunks)

	rec := do(h, http.MethodGet, "/v1/chunks/c1", aliceTok, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "15 days") {
		t.Fatalf("want 200 with content, got %d %s", rec.Code, rec.Body)
	}
}

func TestChunkPreviewUnknownIs404(t *testing.T) {
	h := newTestServer(t, newMemStore(), &fakeAsker{}, fakeChunks{found: false})
	if rec := do(h, http.MethodGet, "/v1/chunks/nope", aliceTok, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

func TestFeedbackOwnerOnly(t *testing.T) {
	st := newMemStore()
	h := newTestServer(t, st, &fakeAsker{result: okResult()}, fakeChunks{})
	conv := createConv(t, h, aliceTok)
	rec := do(h, http.MethodPost, "/v1/conversations/"+conv+"/messages", aliceTok, `{"content":"vacation","stream":false}`)
	var out struct {
		MessageID string `json:"message_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)

	if rec := do(h, http.MethodPost, "/v1/messages/"+out.MessageID+"/feedback", bobTok, `{"rating":1}`); rec.Code != http.StatusNotFound {
		t.Fatalf("non-owner feedback: want 404, got %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/v1/messages/"+out.MessageID+"/feedback", aliceTok, `{"rating":5}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid rating: want 400, got %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/v1/messages/"+out.MessageID+"/feedback", aliceTok, `{"rating":-1,"comment":"wrong"}`); rec.Code != http.StatusCreated {
		t.Fatalf("owner feedback: want 201, got %d", rec.Code)
	}
}

// ---- SSE parsing -----------------------------------------------------------

type sseEvent struct{ name, data string }

func parseSSE(t *testing.T, body string) []sseEvent {
	t.Helper()
	var out []sseEvent
	for _, block := range strings.Split(strings.TrimSpace(body), "\n\n") {
		var ev sseEvent
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				ev.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.data = strings.TrimPrefix(line, "data: ")
			}
		}
		if ev.name != "" {
			out = append(out, ev)
		}
	}
	return out
}
