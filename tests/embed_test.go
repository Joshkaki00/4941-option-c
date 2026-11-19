package tests

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Joshkaki00/4941-option-c/internal/embed"
)

func norm(v []float32) float64 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	return math.Sqrt(s)
}

func TestHashProducesRequestedDimension(t *testing.T) {
	out, err := embed.NewHash(64).Embed(context.Background(), []string{"hello world"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || len(out[0]) != 64 {
		t.Fatalf("shape = %d x %d, want 1 x 64", len(out), len(out[0]))
	}
}

func TestHashVectorsAreUnitLength(t *testing.T) {
	out, _ := embed.NewHash(128).Embed(context.Background(), []string{"full time employees accrue fifteen days"})
	if n := norm(out[0]); math.Abs(n-1) > 1e-5 {
		t.Fatalf("norm = %v, want 1", n)
	}
}

func TestHashEmptyAndPunctuationOnlyAreZeroNotNaN(t *testing.T) {
	out, _ := embed.NewHash(32).Embed(context.Background(), []string{"", "!!! ... ???"})
	for i, v := range out {
		for _, x := range v {
			if math.IsNaN(float64(x)) {
				t.Fatalf("text %d produced NaN", i)
			}
		}
		if norm(v) != 0 {
			t.Fatalf("text %d should be the zero vector, norm=%v", i, norm(v))
		}
	}
}

func TestHashIsDeterministicAndCaseInsensitive(t *testing.T) {
	h := embed.NewHash(256)
	a, _ := h.Embed(context.Background(), []string{"PTO Policy"})
	b, _ := h.Embed(context.Background(), []string{"pto policy"})
	for i := range a[0] {
		if a[0][i] != b[0][i] {
			t.Fatal("hash embedder must be case-insensitive and stable")
		}
	}
}

func TestHashDifferentTextsDiffer(t *testing.T) {
	h := embed.NewHash(256)
	out, _ := h.Embed(context.Background(), []string{"sick leave", "expense reports"})
	same := true
	for i := range out[0] {
		if out[0][i] != out[1][i] {
			same = false
			break
		}
	}
	if same {
		t.Fatal("unrelated texts should not embed identically")
	}
}

func TestHashZeroDimensionReturnsError(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Dim 0 must return an error, not panic: %v", r)
		}
	}()
	if _, err := embed.NewHash(0).Embed(context.Background(), []string{"x"}); err == nil {
		t.Fatal("Dim 0 must be rejected")
	}
}

func TestOllamaSendsModelAndInputAndParsesVectors(t *testing.T) {
	var gotPath string
	var gotBody struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"embeddings":[[1,0,0],[0,1,0]]}`))
	}))
	defer srv.Close()

	o := embed.NewOllama(srv.URL, "nomic-embed-text", 3)
	out, err := o.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/embed" {
		t.Fatalf("path = %s", gotPath)
	}
	if gotBody.Model != "nomic-embed-text" || len(gotBody.Input) != 2 {
		t.Fatalf("body = %+v", gotBody)
	}
	if len(out) != 2 || out[1][1] != 1 {
		t.Fatalf("vectors = %v", out)
	}
}

func TestOllamaTrimsTrailingSlashFromBaseURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"embeddings":[[1]]}`))
	}))
	defer srv.Close()
	if _, err := embed.NewOllama(srv.URL+"/", "m", 1).Embed(context.Background(), []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/embed" {
		t.Fatalf("double slash path: %s", gotPath)
	}
}

func TestOllamaRejectsCountMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"embeddings":[[1,0]]}`))
	}))
	defer srv.Close()
	_, err := embed.NewOllama(srv.URL, "m", 2).Embed(context.Background(), []string{"a", "b"})
	if err == nil || !strings.Contains(err.Error(), "count") {
		t.Fatalf("want count mismatch error, got %v", err)
	}
}

func TestOllamaRejectsWrongDimension(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"embeddings":[[1,0,0]]}`))
	}))
	defer srv.Close()
	_, err := embed.NewOllama(srv.URL, "m", 768).Embed(context.Background(), []string{"a"})
	if err == nil || !strings.Contains(err.Error(), "dimension") {
		t.Fatalf("want dimension error, got %v", err)
	}
}

func TestOllamaSurfacesNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	}))
	defer srv.Close()
	_, err := embed.NewOllama(srv.URL, "missing", 2).Embed(context.Background(), []string{"a"})
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("want status error with 404, got %v", err)
	}
}

func TestOllamaRespectsCancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"embeddings":[[1]]}`))
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := embed.NewOllama(srv.URL, "m", 1).Embed(ctx, []string{"a"}); err == nil {
		t.Fatal("cancelled context must fail the request")
	}
}
