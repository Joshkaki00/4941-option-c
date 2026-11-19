package embed

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func TestHashEmbedderDimensionAndNormalization(t *testing.T) {
	h := NewHash(768)
	vecs, err := h.Embed(context.Background(), []string{"vacation days for full-time employees"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs[0]) != 768 {
		t.Fatalf("want dim 768, got %d", len(vecs[0]))
	}
	var n float64
	for _, v := range vecs[0] {
		n += float64(v) * float64(v)
	}
	if math.Abs(math.Sqrt(n)-1) > 1e-5 {
		t.Fatalf("want unit norm, got %f", math.Sqrt(n))
	}
}

func TestHashEmbedderEmptyTextIsZeroNotNaN(t *testing.T) {
	vecs, err := NewHash(64).Embed(context.Background(), []string{"", "   "})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vecs {
		for _, x := range v {
			if math.IsNaN(float64(x)) {
				t.Fatal("NaN in embedding of empty text")
			}
		}
	}
}

func TestHashEmbedderRanksSharedTermsHigher(t *testing.T) {
	h := NewHash(768)
	vecs, _ := h.Embed(context.Background(), []string{
		"how many vacation days do I get",
		"annual vacation allotment for employees",
		"reimbursement for espresso machines",
	})
	if cosine(vecs[0], vecs[1]) <= cosine(vecs[0], vecs[2]) {
		t.Fatalf("related text should score higher: related=%f unrelated=%f",
			cosine(vecs[0], vecs[1]), cosine(vecs[0], vecs[2]))
	}
}

func TestOllamaEmbedRequestAndResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		vec := make([]float32, 768)
		vec[0] = 1
		out := make([][]float32, len(req.Input))
		for i := range out {
			out[i] = vec
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
	}))
	defer srv.Close()

	o := &Ollama{BaseURL: srv.URL, Model: "nomic-embed-text", Dim: 768, HTTP: srv.Client()}
	vecs, err := o.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 || len(vecs[0]) != 768 {
		t.Fatalf("unexpected shape: %d x %d", len(vecs), len(vecs[0]))
	}
}

func TestOllamaRejectsWrongDimension(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{{1, 2, 3}}})
	}))
	defer srv.Close()

	o := &Ollama{BaseURL: srv.URL, Model: "nomic-embed-text", Dim: 768, HTTP: srv.Client()}
	if _, err := o.Embed(context.Background(), []string{"a"}); err == nil {
		t.Fatal("want error on dimension mismatch")
	}
}

func TestOllamaRejectsCountMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{}})
	}))
	defer srv.Close()

	o := &Ollama{BaseURL: srv.URL, Model: "m", Dim: 768, HTTP: srv.Client()}
	if _, err := o.Embed(context.Background(), []string{"a"}); err == nil {
		t.Fatal("want error when embedding count does not match input")
	}
}
