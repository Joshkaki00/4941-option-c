// Package httpapi exposes the /v1 REST and SSE API (proposal.md §8).
//
// Identity comes only from the bearer token (see internal/auth). Request bodies
// never supply a user id. Answers stream over SSE on the request goroutine.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Joshkaki00/4941-option-c/internal/answer"
	"github.com/Joshkaki00/4941-option-c/internal/auth"
	"github.com/Joshkaki00/4941-option-c/internal/retrieval"
	"github.com/Joshkaki00/4941-option-c/internal/store"
)

const (
	maxBodyBytes     = 16 << 10
	maxQuestionRunes = 1000
	maxCommentRunes  = 1000
)

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Store is the persistence surface used by handlers. *store.Store satisfies it.
type Store interface {
	CreateConversation(ctx context.Context, userSub string) (string, error)
	ConversationOwnedBy(ctx context.Context, convID, userSub string) (bool, error)
	InsertMessage(ctx context.Context, m store.MessageRow, retrieved []store.RetrievedRow) (string, error)
	MessageOwner(ctx context.Context, messageID string) (string, error)
	InsertFeedback(ctx context.Context, messageID, userSub string, rating int, comment string) error
}

// ChunkReader previews a chunk with access re-checked. *retrieval.Searcher satisfies it.
type ChunkReader interface {
	ChunkForUser(ctx context.Context, audiences []string, chunkID string) (retrieval.ChunkView, bool, error)
}

// Asker answers one question. *answer.Service satisfies it.
type Asker interface {
	Ask(ctx context.Context, req answer.Request, onToken func(string) error) (answer.Result, error)
}

// Deps wires the server. Zero values get safe defaults.
type Deps struct {
	Verifier           auth.Verifier
	Store              Store
	Chunks             ChunkReader
	Asker              Asker
	Logger             *slog.Logger
	MaxConcurrent      int           // cap on concurrent asks (default 8)
	AcquireTimeout     time.Duration // wait for a slot before 429 (default 2s)
	StreamWriteTimeout time.Duration // per-response write deadline for SSE (default 2m)
}

// Server holds handlers and the concurrency cap.
type Server struct {
	d   Deps
	sem chan struct{}
}

// New builds a Server with defaults applied.
func New(d Deps) *Server {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.MaxConcurrent <= 0 {
		d.MaxConcurrent = 8
	}
	if d.AcquireTimeout <= 0 {
		d.AcquireTimeout = 2 * time.Second
	}
	if d.StreamWriteTimeout <= 0 {
		d.StreamWriteTimeout = 2 * time.Minute
	}
	return &Server{d: d, sem: make(chan struct{}, d.MaxConcurrent)}
}

// Handler returns the routed HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	authed := func(h http.HandlerFunc) http.Handler { return auth.Require(s.d.Verifier, h) }
	mux.Handle("POST /v1/conversations", authed(s.createConversation))
	mux.Handle("POST /v1/conversations/{id}/messages", authed(s.postMessage))
	mux.Handle("GET /v1/chunks/{id}", authed(s.getChunk))
	mux.Handle("POST /v1/messages/{id}/feedback", authed(s.postFeedback))
	return mux
}

func (s *Server) createConversation(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.FromContext(r.Context())
	convID, err := s.d.Store.CreateConversation(r.Context(), id.Subject)
	if err != nil {
		s.serverError(w, r, "create conversation", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": convID})
}

type questionBody struct {
	Content string `json:"content"`
	Stream  bool   `json:"stream"`
}

type answerBody struct {
	MessageID string         `json:"message_id"`
	Outcome   answer.Outcome `json:"outcome"`
	Answer    string         `json:"answer"`
	Handoff   string         `json:"handoff,omitempty"`
	Citations []citationJSON `json:"citations"`
}

type citationJSON struct {
	Rank          int    `json:"rank"`
	ChunkID       string `json:"chunk_id"`
	DocumentTitle string `json:"document_title"`
	Section       string `json:"section"`
	LastUpdated   string `json:"last_updated"`
}

func (s *Server) postMessage(w http.ResponseWriter, r *http.Request) {
	identity, _ := auth.FromContext(r.Context())
	convID := r.PathValue("id")
	if !uuidRE.MatchString(convID) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	body, ok := decodeQuestion(w, r)
	if !ok {
		return
	}

	owned, err := s.d.Store.ConversationOwnedBy(r.Context(), convID, identity.Subject)
	if err != nil {
		s.serverError(w, r, "check conversation", err)
		return
	}
	if !owned {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	release, ok := s.acquire(r.Context())
	if !ok {
		writeError(w, http.StatusTooManyRequests, "busy, try again")
		return
	}
	defer release()

	req := answer.Request{Question: body.Content, Audiences: identity.Audiences}
	if body.Stream {
		s.streamAnswer(w, r, convID, identity.Subject, req)
		return
	}
	s.jsonAnswer(w, r, convID, identity.Subject, req)
}

func (s *Server) jsonAnswer(w http.ResponseWriter, r *http.Request, convID, sub string, req answer.Request) {
	res, err := s.d.Asker.Ask(r.Context(), req, nil)
	if err != nil {
		s.unavailable(w, r, err)
		return
	}
	msgID, err := s.persist(r.Context(), convID, sub, req.Question, res)
	if err != nil {
		s.serverError(w, r, "persist message", err)
		return
	}
	writeJSON(w, http.StatusOK, answerBody{
		MessageID: msgID, Outcome: res.Outcome, Answer: res.Answer,
		Handoff: res.Handoff, Citations: citationsJSON(res.Citations),
	})
}

// streamAnswer emits: token* → citations → done. The message is persisted before done,
// so the client can use message_id for feedback immediately.
func (s *Server) streamAnswer(w http.ResponseWriter, r *http.Request, convID, sub string, req answer.Request) {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(s.d.StreamWriteTimeout)) // unsupported writers are ignored

	started := false
	start := func() {
		if started {
			return
		}
		started = true
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
	}

	res, err := s.d.Asker.Ask(r.Context(), req, func(text string) error {
		start()
		return writeEvent(w, rc, "token", map[string]string{"text": text})
	})
	if err != nil {
		s.logFailure("ask", r, err)
		if !started {
			writeError(w, http.StatusServiceUnavailable, "answer unavailable")
			return
		}
		_ = writeEvent(w, rc, "error", map[string]string{"error": "answer unavailable"})
		return
	}

	start()
	msgID, err := s.persist(r.Context(), convID, sub, req.Question, res)
	if err != nil {
		s.logFailure("persist message", r, err)
		_ = writeEvent(w, rc, "error", map[string]string{"error": "could not save answer"})
		return
	}
	_ = writeEvent(w, rc, "citations", citationsJSON(res.Citations))
	_ = writeEvent(w, rc, "done", answerBody{
		MessageID: msgID, Outcome: res.Outcome, Answer: res.Answer, Handoff: res.Handoff,
	})
}

func (s *Server) getChunk(w http.ResponseWriter, r *http.Request) {
	identity, _ := auth.FromContext(r.Context())
	view, found, err := s.d.Chunks.ChunkForUser(r.Context(), identity.Audiences, r.PathValue("id"))
	if err != nil {
		s.serverError(w, r, "chunk lookup", err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !view.Allowed {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"chunk_id":       view.ChunkID,
		"document_title": view.Title,
		"section":        strings.Join(view.HeadingPath, " > "),
		"last_updated":   view.LastUpdated.Format("2006-01-02"),
		"content":        view.Content,
	})
}

type feedbackBody struct {
	Rating  int    `json:"rating"`
	Comment string `json:"comment"`
}

func (s *Server) postFeedback(w http.ResponseWriter, r *http.Request) {
	identity, _ := auth.FromContext(r.Context())
	msgID := r.PathValue("id")
	if !uuidRE.MatchString(msgID) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var body feedbackBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.Rating != 1 && body.Rating != -1 {
		writeError(w, http.StatusBadRequest, "rating must be 1 or -1")
		return
	}
	if utf8.RuneCountInString(body.Comment) > maxCommentRunes {
		writeError(w, http.StatusBadRequest, "comment too long")
		return
	}

	owner, err := s.d.Store.MessageOwner(r.Context(), msgID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && owner != identity.Subject) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		s.serverError(w, r, "message owner", err)
		return
	}
	if err := s.d.Store.InsertFeedback(r.Context(), msgID, identity.Subject, body.Rating, body.Comment); err != nil {
		s.serverError(w, r, "insert feedback", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "recorded"})
}

func (s *Server) persist(ctx context.Context, convID, sub, question string, res answer.Result) (string, error) {
	row := store.MessageRow{
		ConversationID: convID,
		UserSub:        sub,
		Question:       question,
		Answer:         res.Answer,
		Outcome:        string(res.Outcome),
		Model:          res.Model,
		LatencyMS:      int(res.Latency.Milliseconds()),
		InputTokens:    res.Usage.InputTokens,
		OutputTokens:   res.Usage.OutputTokens,
	}
	retrieved := make([]store.RetrievedRow, 0, len(res.Retrieved))
	for _, c := range res.Retrieved {
		retrieved = append(retrieved, store.RetrievedRow{
			VersionID: c.VersionID, ChunkID: c.ChunkID, Rank: c.Rank, Score: c.Score, UsedInAnswer: c.Used,
		})
	}
	return s.d.Store.InsertMessage(ctx, row, retrieved)
}

// acquire takes one concurrency slot, waiting briefly, then fails so the caller can return 429.
func (s *Server) acquire(ctx context.Context) (func(), bool) {
	timer := time.NewTimer(s.d.AcquireTimeout)
	defer timer.Stop()
	select {
	case s.sem <- struct{}{}:
		return func() { <-s.sem }, true
	case <-timer.C:
		return nil, false
	case <-ctx.Done():
		return nil, false
	}
}

func decodeQuestion(w http.ResponseWriter, r *http.Request) (questionBody, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var body questionBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return body, false
	}
	body.Content = strings.TrimSpace(body.Content)
	if body.Content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return body, false
	}
	if utf8.RuneCountInString(body.Content) > maxQuestionRunes {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("content must be at most %d characters", maxQuestionRunes))
		return body, false
	}
	return body, true
}

func citationsJSON(cs []answer.Citation) []citationJSON {
	out := make([]citationJSON, 0, len(cs))
	for _, c := range cs {
		out = append(out, citationJSON{
			Rank:          c.Rank,
			ChunkID:       c.ChunkID,
			DocumentTitle: c.DocumentTitle,
			Section:       c.Section,
			LastUpdated:   c.LastUpdated.Format("2006-01-02"),
		})
	}
	return out
}

func (s *Server) unavailable(w http.ResponseWriter, r *http.Request, err error) {
	s.logFailure("ask", r, err)
	writeError(w, http.StatusServiceUnavailable, "answer unavailable")
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, op string, err error) {
	s.logFailure(op, r, err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

// logFailure logs the route and error only. Never log question text (sensitive disclosures).
func (s *Server) logFailure(op string, r *http.Request, err error) {
	s.d.Logger.Error("request failed", "op", op, "route", r.Method+" "+r.URL.Path, "err", err)
}

func writeEvent(w http.ResponseWriter, rc *http.ResponseController, event string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
		return err
	}
	return rc.Flush()
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
