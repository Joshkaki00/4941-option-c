package admin_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Joshkaki00/4941-option-c/internal/admin"
	"github.com/Joshkaki00/4941-option-c/internal/auth"
	"github.com/Joshkaki00/4941-option-c/internal/store"
)

const secret = "0123456789abcdef0123456789abcdef"

var tokens = auth.StaticTokens{
	"admin-tok": {Subject: "ops", Audiences: []string{"all-employees", admin.Audience}},
	"emp-tok":   {Subject: "alice", Audiences: []string{"all-employees"}},
}

type fakeStore struct {
	jobs      map[int64]store.JobStatus
	enqueued  []string
	nextID    int64
	outcomes  map[string]int
	questions []store.QuestionCount
	gotLimit  int
	enqErr    error
}

func (f *fakeStore) EnqueueIngestion(_ context.Context, path string) (int64, error) {
	if f.enqErr != nil {
		return 0, f.enqErr
	}
	f.nextID++
	f.enqueued = append(f.enqueued, path)
	if f.jobs == nil {
		f.jobs = map[int64]store.JobStatus{}
	}
	f.jobs[f.nextID] = store.JobStatus{ID: f.nextID, SourcePath: path, Status: "queued"}
	return f.nextID, nil
}

func (f *fakeStore) GetJob(_ context.Context, id int64) (store.JobStatus, error) {
	j, ok := f.jobs[id]
	if !ok {
		return store.JobStatus{}, store.ErrNotFound
	}
	return j, nil
}

func (f *fakeStore) OutcomeSummary(context.Context) (map[string]int, error) {
	return f.outcomes, nil
}

func (f *fakeStore) UnansweredQuestions(_ context.Context, limit int) ([]store.QuestionCount, error) {
	f.gotLimit = limit
	return f.questions, nil
}

func newServer(t *testing.T, st *fakeStore, webhookSecret []byte) *httptest.Server {
	t.Helper()
	h := admin.New(admin.Deps{
		Verifier:      tokens,
		Store:         st,
		EnqueueCorpus: func(context.Context) (int, error) { return 3, nil },
		WebhookSecret: webhookSecret,
	})
	srv := httptest.NewServer(h.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, method, url, bearer string, body []byte, headers map[string]string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.Bytes()
}

func sign(body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// --- admin routes: authentication and authorization ---

func TestAdminRoutesRequireBearerToken(t *testing.T) {
	srv := newServer(t, &fakeStore{}, nil)
	code, _ := do(t, http.MethodPost, srv.URL+"/v1/admin/ingestion/runs", "", nil, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("no token: status %d, want 401", code)
	}
}

func TestAdminRoutesRejectNonAdmin(t *testing.T) {
	srv := newServer(t, &fakeStore{}, nil)
	code, _ := do(t, http.MethodPost, srv.URL+"/v1/admin/ingestion/runs", "emp-tok", nil, nil)
	if code != http.StatusForbidden {
		t.Fatalf("employee token: status %d, want 403", code)
	}
}

func TestEnqueueCorpusReturns202WithCount(t *testing.T) {
	srv := newServer(t, &fakeStore{}, nil)
	code, body := do(t, http.MethodPost, srv.URL+"/v1/admin/ingestion/runs", "admin-tok", nil, nil)
	if code != http.StatusAccepted {
		t.Fatalf("status %d, want 202", code)
	}
	var out map[string]int
	if err := json.Unmarshal(body, &out); err != nil || out["queued"] != 3 {
		t.Fatalf("body = %s", body)
	}
}

func TestEnqueueCorpusReportsFailure(t *testing.T) {
	h := admin.New(admin.Deps{
		Verifier:      tokens,
		Store:         &fakeStore{},
		EnqueueCorpus: func(context.Context) (int, error) { return 0, errors.New("disk gone") },
	})
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	code, _ := do(t, http.MethodPost, srv.URL+"/v1/admin/ingestion/runs", "admin-tok", nil, nil)
	if code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", code)
	}
}

func TestGetRunReturnsJobStatus(t *testing.T) {
	st := &fakeStore{}
	id, _ := st.EnqueueIngestion(context.Background(), "pto-policy.md")
	srv := newServer(t, st, nil)

	code, body := do(t, http.MethodGet, srv.URL+"/v1/admin/ingestion/runs/"+itoa(id), "admin-tok", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	if !strings.Contains(string(body), `"source_path":"pto-policy.md"`) || !strings.Contains(string(body), `"status":"queued"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestGetRunUnknownIs404AndBadIDIs400(t *testing.T) {
	srv := newServer(t, &fakeStore{}, nil)
	if code, _ := do(t, http.MethodGet, srv.URL+"/v1/admin/ingestion/runs/999", "admin-tok", nil, nil); code != http.StatusNotFound {
		t.Fatalf("unknown run: status %d, want 404", code)
	}
	if code, _ := do(t, http.MethodGet, srv.URL+"/v1/admin/ingestion/runs/abc", "admin-tok", nil, nil); code != http.StatusBadRequest {
		t.Fatalf("non-numeric run id: status %d, want 400", code)
	}
}

func TestAnalyticsSummaryIncludesTotal(t *testing.T) {
	st := &fakeStore{outcomes: map[string]int{"answered": 4, "declined_no_source": 1}}
	srv := newServer(t, st, nil)
	code, body := do(t, http.MethodGet, srv.URL+"/v1/admin/analytics/summary", "admin-tok", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	var out struct {
		Outcomes map[string]int `json:"outcomes"`
		Total    int            `json:"total"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Total != 5 || out.Outcomes["answered"] != 4 {
		t.Fatalf("summary = %+v", out)
	}
}

func TestUnansweredReportIsAggregatedAndLimited(t *testing.T) {
	st := &fakeStore{questions: []store.QuestionCount{{Question: "espresso machine?", Count: 2}}}
	srv := newServer(t, st, nil)

	code, body := do(t, http.MethodGet, srv.URL+"/v1/admin/analytics/unanswered?limit=5", "admin-tok", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if st.gotLimit != 5 {
		t.Fatalf("limit passed = %d, want 5", st.gotLimit)
	}
	if strings.Contains(string(body), "user") || strings.Contains(string(body), "alice") {
		t.Fatalf("report must carry no identities: %s", body)
	}
	if !strings.Contains(string(body), "espresso machine?") {
		t.Fatalf("body = %s", body)
	}
}

func TestUnansweredDefaultsLimit(t *testing.T) {
	st := &fakeStore{}
	srv := newServer(t, st, nil)
	do(t, http.MethodGet, srv.URL+"/v1/admin/analytics/unanswered", "admin-tok", nil, nil)
	if st.gotLimit != 20 {
		t.Fatalf("default limit = %d, want 20", st.gotLimit)
	}
}

// --- webhook: HMAC signature, no bearer token ---

func TestWebhookDisabledWithoutSecret(t *testing.T) {
	srv := newServer(t, &fakeStore{}, nil)
	body := []byte(`{"source_path":"pto-policy.md"}`)
	code, _ := do(t, http.MethodPost, srv.URL+"/v1/admin/webhooks/source-changed", "", body,
		map[string]string{"X-Handbook-Signature": sign(body)})
	if code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured webhook: status %d, want 503", code)
	}
}

func TestWebhookRejectsMissingOrBadSignature(t *testing.T) {
	st := &fakeStore{}
	srv := newServer(t, st, []byte(secret))
	body := []byte(`{"source_path":"pto-policy.md"}`)
	for name, sig := range map[string]string{
		"missing":   "",
		"wrong key": sign([]byte(`{"source_path":"other.md"}`)),
		"no prefix": strings.TrimPrefix(sign(body), "sha256="),
	} {
		headers := map[string]string{}
		if sig != "" {
			headers["X-Handbook-Signature"] = sig
		}
		code, _ := do(t, http.MethodPost, srv.URL+"/v1/admin/webhooks/source-changed", "", body, headers)
		if code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, code)
		}
	}
	if len(st.enqueued) != 0 {
		t.Fatalf("rejected webhook must not enqueue, got %v", st.enqueued)
	}
}

func TestWebhookValidSignatureEnqueuesAndReturns202(t *testing.T) {
	st := &fakeStore{}
	srv := newServer(t, st, []byte(secret))
	body := []byte(`{"source_path":"pto-policy.md"}`)
	code, out := do(t, http.MethodPost, srv.URL+"/v1/admin/webhooks/source-changed", "", body,
		map[string]string{"X-Handbook-Signature": sign(body)})
	if code != http.StatusAccepted {
		t.Fatalf("status %d, want 202", code)
	}
	if len(st.enqueued) != 1 || st.enqueued[0] != "pto-policy.md" {
		t.Fatalf("enqueued = %v", st.enqueued)
	}
	if !strings.Contains(string(out), `"job_id"`) {
		t.Fatalf("body = %s", out)
	}
}

func TestWebhookRejectsUnsafeSourcePaths(t *testing.T) {
	st := &fakeStore{}
	srv := newServer(t, st, []byte(secret))
	for _, p := range []string{"../etc/passwd.md", "/abs/x.md", "notes.txt", "a\\b.md", "", "sub/../x.md"} {
		body, _ := json.Marshal(map[string]string{"source_path": p})
		code, _ := do(t, http.MethodPost, srv.URL+"/v1/admin/webhooks/source-changed", "", body,
			map[string]string{"X-Handbook-Signature": sign(body)})
		if code != http.StatusBadRequest {
			t.Errorf("path %q: status %d, want 400", p, code)
		}
	}
	if len(st.enqueued) != 0 {
		t.Fatalf("unsafe paths must not enqueue, got %v", st.enqueued)
	}
}

func TestWebhookRejectsOversizedAndBadJSON(t *testing.T) {
	srv := newServer(t, &fakeStore{}, []byte(secret))
	big := []byte(`{"source_path":"` + strings.Repeat("a", 20<<10) + `.md"}`)
	code, _ := do(t, http.MethodPost, srv.URL+"/v1/admin/webhooks/source-changed", "", big,
		map[string]string{"X-Handbook-Signature": sign(big)})
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: status %d, want 413", code)
	}
	bad := []byte(`{not json`)
	code, _ = do(t, http.MethodPost, srv.URL+"/v1/admin/webhooks/source-changed", "", bad,
		map[string]string{"X-Handbook-Signature": sign(bad)})
	if code != http.StatusBadRequest {
		t.Fatalf("bad json: status %d, want 400", code)
	}
}

func TestValidSourcePath(t *testing.T) {
	good := []string{"pto-policy.md", "hr/leave.md", "Notes.MD"}
	bad := []string{"", "../x.md", "a/../b.md", "/abs.md", "x.txt", "back\\slash.md", "x.md\x00"}
	for _, p := range good {
		if !admin.ValidSourcePath(p) {
			t.Errorf("%q should be valid", p)
		}
	}
	for _, p := range bad {
		if admin.ValidSourcePath(p) {
			t.Errorf("%q should be invalid", p)
		}
	}
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
