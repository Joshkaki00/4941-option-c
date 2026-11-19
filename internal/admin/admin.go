// Package admin serves /v1/admin: ingestion runs, the signed source-changed webhook,
// and handbook-gap analytics (proposal.md §8).
package admin

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/Joshkaki00/4941-option-c/internal/auth"
	"github.com/Joshkaki00/4941-option-c/internal/store"
)

// Audience is required, in addition to a valid token, for every /v1/admin route
// except the webhook. Granted through SSO groups.
const Audience = "handbook-admin"

const (
	maxWebhookBytes    = 16 << 10
	signatureHeader    = "X-Handbook-Signature"
	signaturePrefix    = "sha256="
	defaultUnansweredN = 20
)

// Store is the persistence surface for admin routes. *store.Store satisfies it.
type Store interface {
	EnqueueIngestion(ctx context.Context, sourcePath string) (int64, error)
	GetJob(ctx context.Context, id int64) (store.JobStatus, error)
	OutcomeSummary(ctx context.Context) (map[string]int, error)
	UnansweredQuestions(ctx context.Context, limit int) ([]store.QuestionCount, error)
}

// Deps wires the admin routes.
type Deps struct {
	Verifier auth.Verifier
	Store    Store
	// EnqueueCorpus queues every source file for ingestion and returns how many it queued.
	EnqueueCorpus func(ctx context.Context) (int, error)
	// WebhookSecret signs source-changed webhooks (HMAC-SHA256). Empty disables the webhook.
	WebhookSecret []byte
	Logger        *slog.Logger
}

// Handler serves the admin routes.
type Handler struct{ d Deps }

// New returns the admin handler.
func New(d Deps) *Handler {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &Handler{d: d}
}

// Handler returns the routed admin mux.
func (h *Handler) Handler() http.Handler {
	mux := http.NewServeMux()
	admin := func(f http.HandlerFunc) http.Handler {
		return auth.Require(h.d.Verifier, requireAudience(Audience, f))
	}
	mux.Handle("POST /v1/admin/ingestion/runs", admin(h.enqueueCorpus))
	mux.Handle("GET /v1/admin/ingestion/runs/{id}", admin(h.getRun))
	mux.Handle("GET /v1/admin/analytics/summary", admin(h.summary))
	mux.Handle("GET /v1/admin/analytics/unanswered", admin(h.unanswered))
	mux.HandleFunc("POST /v1/admin/webhooks/source-changed", h.sourceChanged)
	return mux
}

// requireAudience wraps next so that only identities carrying aud get through; others get 403.
func requireAudience(aud string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := auth.FromContext(r.Context())
		if !ok || !slices.Contains(id.Audiences, aud) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		next(w, r)
	}
}

// enqueueCorpus queues every corpus file for ingestion and reports how many it queued.
func (h *Handler) enqueueCorpus(w http.ResponseWriter, r *http.Request) {
	n, err := h.d.EnqueueCorpus(r.Context())
	if err != nil {
		h.d.Logger.Error("enqueue corpus failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not queue ingestion"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]int{"queued": n})
}

type jobJSON struct {
	ID         int64  `json:"id"`
	SourcePath string `json:"source_path"`
	Status     string `json:"status"`
	Attempts   int    `json:"attempts"`
	LastError  string `json:"last_error"`
}

// getRun reports the status of one ingestion job, identified by its positive integer ID.
func (h *Handler) getRun(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run id must be a positive integer"})
		return
	}
	job, err := h.d.Store.GetJob(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "run not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load run"})
		return
	}
	writeJSON(w, http.StatusOK, jobJSON{
		ID: job.ID, SourcePath: job.SourcePath, Status: job.Status,
		Attempts: job.Attempts, LastError: job.LastError,
	})
}

// summary reports how many messages ended in each outcome, plus the total.
func (h *Handler) summary(w http.ResponseWriter, r *http.Request) {
	counts, err := h.d.Store.OutcomeSummary(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load summary"})
		return
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	writeJSON(w, http.StatusOK, map[string]any{"outcomes": counts, "total": total})
}

type questionJSON struct {
	Question string `json:"question"`
	Count    int    `json:"count"`
}

// unanswered lists the most-asked questions that were not answered from the handbook. limit defaults to 20.
func (h *Handler) unanswered(w http.ResponseWriter, r *http.Request) {
	limit := defaultUnansweredN
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be a positive integer"})
			return
		}
		limit = n
	}
	rows, err := h.d.Store.UnansweredQuestions(r.Context(), limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load report"})
		return
	}
	out := make([]questionJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, questionJSON{Question: row.Question, Count: row.Count})
	}
	writeJSON(w, http.StatusOK, map[string]any{"questions": out})
}

// sourceChanged verifies the HMAC signature over the raw body before trusting any of it.
func (h *Handler) sourceChanged(w http.ResponseWriter, r *http.Request) {
	if len(h.d.WebhookSecret) == 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "webhook not configured"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "body too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read body"})
		return
	}
	if !validSignature(h.d.WebhookSecret, body, r.Header.Get(signatureHeader)) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid signature"})
		return
	}
	var req struct {
		SourcePath string `json:"source_path"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body must be JSON"})
		return
	}
	if !ValidSourcePath(req.SourcePath) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "source_path must be a relative .md path inside the corpus"})
		return
	}
	id, err := h.d.Store.EnqueueIngestion(r.Context(), req.SourcePath)
	if err != nil {
		h.d.Logger.Error("enqueue webhook job failed", "source", req.SourcePath, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not queue ingestion"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]int64{"job_id": id})
}

// validSignature checks "sha256=<hex>" against HMAC-SHA256(secret, body) in constant time.
func validSignature(secret, body []byte, header string) bool {
	hexSig, ok := strings.CutPrefix(header, signaturePrefix)
	if !ok {
		return false
	}
	got, err := hex.DecodeString(hexSig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

// ValidSourcePath accepts a relative, slash-separated .md path with no traversal.
// Paths are later resolved under the corpus root, but this rejects them early.
func ValidSourcePath(p string) bool {
	if p == "" || len(p) > 255 || strings.ContainsAny(p, "\\\x00") || strings.HasPrefix(p, "/") {
		return false
	}
	if !strings.EqualFold(path.Ext(p), ".md") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// writeJSON writes v as a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
