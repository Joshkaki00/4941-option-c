// Package ingest turns handbook Markdown into published, embedded document versions.
// Work runs through the durable ingestion_jobs queue (proposal.md §6.2).
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Joshkaki00/4941-option-c/internal/chunk"
	"github.com/Joshkaki00/4941-option-c/internal/embed"
	"github.com/Joshkaki00/4941-option-c/internal/store"
)

// Doc is a parsed source document: front matter plus Markdown body.
type Doc struct {
	Title       string
	Owner       string
	LastUpdated time.Time
	Audiences   []string
	Body        string
}

// ParseDocument reads the front-matter block:
//
//	---
//	title: PTO Policy
//	owner: People Operations
//	last_updated: 2026-03-01
//	audiences: all-employees, us-ca
//	---
//
// title and last_updated are required. Documents without audiences are indexed
// but invisible to search (default-deny).
func ParseDocument(raw []byte) (Doc, error) {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return Doc{}, errors.New("ingest: missing front matter")
	}
	rest := text[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return Doc{}, errors.New("ingest: unterminated front matter")
	}
	header := rest[:end]
	body := strings.TrimPrefix(rest[end+len("\n---"):], "\n")

	fields := map[string]string{}
	for _, line := range strings.Split(header, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}

	doc := Doc{Title: fields["title"], Owner: fields["owner"], Body: body}
	if doc.Title == "" {
		return Doc{}, errors.New("ingest: front matter needs title")
	}
	date, err := time.Parse("2006-01-02", fields["last_updated"])
	if err != nil {
		return Doc{}, fmt.Errorf("ingest: last_updated must be YYYY-MM-DD: %w", err)
	}
	doc.LastUpdated = date
	for _, a := range strings.Split(fields["audiences"], ",") {
		if a = strings.TrimSpace(a); a != "" {
			doc.Audiences = append(doc.Audiences, a)
		}
	}
	return doc, nil
}

// Prepare chunks and embeds one raw document into a publishable version.
func Prepare(ctx context.Context, sourceID string, raw []byte, emb embed.Embedder) (store.DocumentVersion, error) {
	doc, err := ParseDocument(raw)
	if err != nil {
		return store.DocumentVersion{}, err
	}
	chunks := chunk.Split(sourceID, doc.Title, doc.Body)
	if len(chunks) == 0 {
		return store.DocumentVersion{}, fmt.Errorf("ingest: %s has no content", sourceID)
	}
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Content
	}
	vecs, err := emb.Embed(ctx, texts)
	if err != nil {
		return store.DocumentVersion{}, fmt.Errorf("embed %s: %w", sourceID, err)
	}
	if len(vecs) != len(chunks) {
		return store.DocumentVersion{}, fmt.Errorf("embed %s: got %d vectors for %d chunks", sourceID, len(vecs), len(chunks))
	}
	inputs := make([]store.ChunkInput, len(chunks))
	for i, c := range chunks {
		inputs[i] = store.ChunkInput{
			ID: c.ID, Ordinal: c.Ordinal, HeadingPath: c.HeadingPath,
			Content: c.Content, ChunkType: c.Type, Embedding: vecs[i],
		}
	}
	sum := sha256.Sum256(raw)
	return store.DocumentVersion{
		SourceID:    sourceID,
		Title:       doc.Title,
		Owner:       doc.Owner,
		LastUpdated: doc.LastUpdated,
		ContentHash: hex.EncodeToString(sum[:]),
		Audiences:   doc.Audiences,
		Chunks:      inputs,
	}, nil
}

// Worker drains the ingestion queue. Root is the corpus directory that job paths are relative to.
type Worker struct {
	Store       *store.Store
	Embedder    embed.Embedder
	Root        string
	Concurrency int // bounded fan-out (default 2; respects embedding API rate limits)
	MaxAttempts int // before a job is marked failed (default 5)
	Logger      *slog.Logger
}

// IngestFile processes one job path relative to Root.
func (w *Worker) IngestFile(ctx context.Context, rel string) error {
	path, err := w.resolve(rel)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", rel, err)
	}
	v, err := Prepare(ctx, filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel))), raw, w.Embedder)
	if err != nil {
		return err
	}
	if _, err := w.Store.PublishVersion(ctx, v); err != nil && !errors.Is(err, store.ErrUnchanged) {
		return err
	}
	return nil
}

// Drain claims and runs jobs until none are runnable. Each job runs in a bounded goroutine,
// and all of them are joined before Drain returns. A failing job is recorded and does not
// stop the others. Retries scheduled for later stay queued for the next drain.
func (w *Worker) Drain(ctx context.Context) (done, failed int, err error) {
	concurrency := w.Concurrency
	if concurrency <= 0 {
		concurrency = 2
	}
	maxAttempts := w.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	logger := w.Logger
	if logger == nil {
		logger = slog.Default()
	}

	var nDone, nFailed atomic.Int64
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)

	var claimErr error
	for gctx.Err() == nil {
		job, err := w.Store.ClaimJob(gctx)
		if err != nil {
			claimErr = err
			break
		}
		if job == nil {
			break
		}
		j := *job
		// g.Go blocks while `concurrency` jobs are running, which bounds claimed work.
		g.Go(func() error {
			runErr := w.IngestFile(gctx, j.SourcePath)
			// Record the outcome even if the drain was cancelled, so no job stays "running".
			if finishErr := w.Store.FinishJob(context.WithoutCancel(gctx), j, runErr, maxAttempts); finishErr != nil {
				logger.Error("finish job failed", "job", j.ID, "err", finishErr)
			}
			if runErr != nil {
				nFailed.Add(1)
				logger.Error("ingest failed", "job", j.ID, "source", j.SourcePath, "attempt", j.Attempts, "err", runErr)
			} else {
				nDone.Add(1)
			}
			return nil
		})
	}
	_ = g.Wait()

	done, failed = int(nDone.Load()), int(nFailed.Load())
	if claimErr != nil {
		return done, failed, claimErr
	}
	return done, failed, ctx.Err()
}

// EnqueueDir queues every .md file under root (hidden directories skipped).
func EnqueueDir(ctx context.Context, st *store.Store, root string) (int, error) {
	count := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, err := st.EnqueueIngestion(ctx, filepath.ToSlash(rel)); err != nil {
			return err
		}
		count++
		return nil
	})
	return count, err
}

// resolve rejects paths that escape Root.
func (w *Worker) resolve(rel string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("ingest: path escapes corpus root: %q", rel)
	}
	return filepath.Join(w.Root, clean), nil
}
