// Package embed turns text into vectors. The same model must embed both
// documents at ingest time and questions at ask time.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode"
)

// Embedder produces one vector per input text.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Hash is a deterministic, offline embedder based on hashed unigrams.
// It is lexical, not semantic. Use it for tests and offline development only.
type Hash struct {
	Dim int
}

// NewHash returns a hashing embedder with the given dimension.
func NewHash(dim int) *Hash { return &Hash{Dim: dim} }

// Embed implements Embedder.
func (h *Hash) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if h.Dim <= 0 {
		return nil, errors.New("hash embed: dimension must be positive")
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = h.one(t)
	}
	return out, nil
}

// one embeds a single text. Lowercased tokens are hashed into buckets with a sign, then the vector is normalized to unit length. Empty text gives the zero vector.
func (h *Hash) one(text string) []float32 {
	vec := make([]float32, h.Dim)
	tokens := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, tok := range tokens {
		f := fnv.New64a()
		_, _ = f.Write([]byte(tok))
		sum := f.Sum64()
		idx := int(sum % uint64(h.Dim))
		sign := float32(1)
		if sum>>63 == 1 {
			sign = -1
		}
		vec[idx] += sign
	}
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	if norm == 0 {
		return vec
	}
	scale := float32(1 / math.Sqrt(norm))
	for i := range vec {
		vec[i] *= scale
	}
	return vec
}

// Ollama calls a local Ollama server (POST /api/embed).
type Ollama struct {
	BaseURL string
	Model   string
	Dim     int
	HTTP    *http.Client
}

// NewOllama returns an Ollama embedder with a bounded HTTP timeout.
func NewOllama(baseURL, model string, dim int) *Ollama {
	return &Ollama{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Model:   model,
		Dim:     dim,
		HTTP:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Embed implements Embedder. It validates count and dimension of the response.
func (o *Ollama) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(map[string]any{"model": o.Model, "input": texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("ollama embed: status %d: %s", resp.StatusCode, msg)
	}
	var parsed struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("ollama embed: decode: %w", err)
	}
	if len(parsed.Embeddings) != len(texts) {
		return nil, errors.New("ollama embed: count mismatch")
	}
	for _, v := range parsed.Embeddings {
		if len(v) != o.Dim {
			return nil, fmt.Errorf("ollama embed: dimension %d, want %d", len(v), o.Dim)
		}
	}
	return parsed.Embeddings, nil
}
