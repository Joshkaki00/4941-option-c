// Package retrieval runs audience-filtered hybrid search (vector + full-text, RRF).
//
// Access control lives in the SQL: every query joins document_audiences and
// requires an audience the caller holds. No audience row means no result.
package retrieval

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"

	"github.com/Joshkaki00/4941-option-c/internal/embed"
	"github.com/Joshkaki00/4941-option-c/internal/store"
)

// Candidate is one retrieved chunk.
type Candidate struct {
	ChunkID     string
	VersionID   int64
	DocumentID  int64
	SourceID    string
	Title       string
	HeadingPath []string
	Content     string
	ChunkType   string
	LastUpdated time.Time
	Score       float64 // similarity (vector leg) or fused RRF score
}

// Result is the outcome of one hybrid search.
type Result struct {
	Candidates  []Candidate // fused, best first, at most limit
	KeywordHits int         // size of the keyword leg
	BestSim     float64     // best vector similarity; 0 when the vector leg produced nothing
	Degraded    bool        // one leg failed; results come from the other leg
}

// Searcher runs queries against the store.
type Searcher struct {
	pool     *store.Store
	embedder embed.Embedder
}

// New returns a Searcher.
func New(st *store.Store, e embed.Embedder) *Searcher {
	return &Searcher{pool: st, embedder: e}
}

const candidateCols = `c.id, c.version_id, c.document_id, d.source_id, d.title,
	c.heading_path, c.content, c.chunk_type, dv.last_updated`

const fromClause = `
	FROM chunks c
	JOIN document_versions dv ON dv.id = c.version_id AND dv.is_current
	JOIN documents d ON d.id = c.document_id`

// audienceClause is the privacy gate. Default-deny: no matching row, no result.
const audienceClause = `
	AND EXISTS (SELECT 1 FROM document_audiences da
	            WHERE da.document_id = c.document_id AND da.audience_id = ANY($1))`

// Hybrid runs keyword and vector legs in parallel and fuses them with RRF.
// A failed leg degrades to the other one; both failing is an error.
func (s *Searcher) Hybrid(ctx context.Context, audiences []string, query string, limit int) (Result, error) {
	if len(audiences) == 0 || limit <= 0 {
		return Result{}, nil
	}

	tsq := keywordQuery(query)
	var (
		kw, vec       []Candidate
		kwErr, vecErr error
		wg            sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		if tsq == "" {
			return
		}
		kw, kwErr = s.keyword(ctx, audiences, tsq, limit)
	}()
	go func() {
		defer wg.Done()
		vecs, err := s.embedder.Embed(ctx, []string{query})
		if err != nil {
			vecErr = fmt.Errorf("embed query: %w", err)
			return
		}
		vec, vecErr = s.vector(ctx, audiences, vecs[0], limit)
	}()
	wg.Wait()

	if kwErr != nil && vecErr != nil {
		return Result{}, errors.Join(kwErr, vecErr)
	}

	var best float64
	for _, c := range vec {
		if c.Score > best {
			best = c.Score
		}
	}
	return Result{
		Candidates:  Fuse(limit, kw, vec),
		KeywordHits: len(kw),
		BestSim:     best,
		Degraded:    kwErr != nil || vecErr != nil,
	}, nil
}

func (s *Searcher) keyword(ctx context.Context, audiences []string, tsq string, limit int) ([]Candidate, error) {
	rows, err := s.pool.Pool.Query(ctx, `
		SELECT `+candidateCols+`, ts_rank(c.tsv, to_tsquery('english', $2)) AS score`+fromClause+`
		WHERE c.is_current AND c.tsv @@ to_tsquery('english', $2)`+audienceClause+`
		ORDER BY score DESC
		LIMIT $3`, audiences, tsq, limit)
	if err != nil {
		return nil, fmt.Errorf("keyword search: %w", err)
	}
	return collect(rows)
}

func (s *Searcher) vector(ctx context.Context, audiences []string, q []float32, limit int) ([]Candidate, error) {
	rows, err := s.pool.Pool.Query(ctx, `
		SELECT `+candidateCols+`, 1 - (c.embedding <=> $2) AS score`+fromClause+`
		WHERE c.is_current`+audienceClause+`
		ORDER BY c.embedding <=> $2
		LIMIT $3`, audiences, pgvector.NewVector(q), limit)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}
	return collect(rows)
}

// ChunkView is a preview of one chunk for a specific caller.
type ChunkView struct {
	Candidate
	Allowed bool // false means the caller may not see this chunk; Content is empty
}

// ChunkForUser re-checks access for a single chunk (GET /v1/chunks/{id}).
// found is false when the id does not exist in the current index.
func (s *Searcher) ChunkForUser(ctx context.Context, audiences []string, chunkID string) (ChunkView, bool, error) {
	var (
		v       ChunkView
		allowed bool
	)
	err := s.pool.Pool.QueryRow(ctx, `
		SELECT `+candidateCols+`,
		       EXISTS (SELECT 1 FROM document_audiences da
		               WHERE da.document_id = c.document_id AND da.audience_id = ANY($2))`+fromClause+`
		WHERE c.id = $1 AND c.is_current`,
		chunkID, audiences,
	).Scan(&v.ChunkID, &v.VersionID, &v.DocumentID, &v.SourceID, &v.Title,
		&v.HeadingPath, &v.Content, &v.ChunkType, &v.LastUpdated, &allowed)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChunkView{}, false, nil
	}
	if err != nil {
		return ChunkView{}, false, fmt.Errorf("chunk lookup: %w", err)
	}
	v.Allowed = allowed && len(audiences) > 0
	if !v.Allowed {
		v.Content = ""
		v.HeadingPath = nil
	}
	return v, true, nil
}

func collect(rows pgx.Rows) ([]Candidate, error) {
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var c Candidate
		if err := rows.Scan(&c.ChunkID, &c.VersionID, &c.DocumentID, &c.SourceID, &c.Title,
			&c.HeadingPath, &c.Content, &c.ChunkType, &c.LastUpdated, &c.Score); err != nil {
			return nil, fmt.Errorf("scan candidate: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

var (
	tokenRE   = regexp.MustCompile(`[a-z0-9]+`)
	stopwords = map[string]bool{
		"a": true, "an": true, "the": true, "and": true, "or": true, "of": true, "to": true,
		"in": true, "on": true, "for": true, "is": true, "are": true, "be": true, "do": true,
		"does": true, "i": true, "my": true, "me": true, "get": true, "how": true, "what": true,
		"when": true, "where": true, "can": true, "with": true, "from": true, "at": true,
		"by": true, "it": true, "this": true, "that": true,
	}
)

// QueryTerms returns the distinct, lowercased, non-stopword terms of a question.
// Terms are [a-z0-9] only, so they cannot inject tsquery operators.
func QueryTerms(q string) []string {
	var terms []string
	seen := map[string]bool{}
	for _, tok := range tokenRE.FindAllString(strings.ToLower(q), -1) {
		if len(tok) < 2 || stopwords[tok] || seen[tok] {
			continue
		}
		seen[tok] = true
		terms = append(terms, tok)
	}
	return terms
}

// keywordQuery builds an OR-query for to_tsquery from QueryTerms.
func keywordQuery(q string) string {
	return strings.Join(QueryTerms(q), " | ")
}
