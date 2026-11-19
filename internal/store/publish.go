package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"
)

// ErrUnchanged means the content hash matches the current version; nothing was written.
var ErrUnchanged = errors.New("store: document content unchanged")

// ChunkInput is one embedded chunk ready to be published.
type ChunkInput struct {
	ID          string
	Ordinal     int
	HeadingPath []string
	Content     string
	ChunkType   string
	Embedding   []float32
}

// DocumentVersion is a full new version of one source document.
type DocumentVersion struct {
	SourceID    string
	Title       string
	Owner       string
	LastUpdated time.Time
	ContentHash string
	Audiences   []string // empty means invisible to search (default-deny)
	Chunks      []ChunkInput
}

// PublishVersion writes a new version and flips it current in one transaction.
// Old chunks stay for audit but are excluded from search by is_current.
func (s *Store) PublishVersion(ctx context.Context, v DocumentVersion) (int64, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after commit

	var docID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO documents (source_id, title, owner) VALUES ($1, $2, $3)
		ON CONFLICT (source_id) DO UPDATE SET title = EXCLUDED.title, owner = EXCLUDED.owner
		RETURNING id`, v.SourceID, v.Title, v.Owner).Scan(&docID); err != nil {
		return 0, fmt.Errorf("upsert document: %w", err)
	}

	var currentHash string
	err = tx.QueryRow(ctx, `
		SELECT content_hash FROM document_versions WHERE document_id = $1 AND is_current`, docID).Scan(&currentHash)
	if err == nil && currentHash == v.ContentHash {
		return 0, ErrUnchanged
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}

	if _, err := tx.Exec(ctx, `UPDATE chunks SET is_current = false WHERE document_id = $1 AND is_current`, docID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE document_versions SET is_current = false WHERE document_id = $1 AND is_current`, docID); err != nil {
		return 0, err
	}

	var versionID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO document_versions (document_id, content_hash, last_updated, is_current)
		VALUES ($1, $2, $3, true) RETURNING id`, docID, v.ContentHash, v.LastUpdated).Scan(&versionID); err != nil {
		return 0, fmt.Errorf("insert version: %w", err)
	}

	for _, c := range v.Chunks {
		// heading_path is NOT NULL; a nil slice would be stored as NULL.
		if c.HeadingPath == nil {
			c.HeadingPath = []string{}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO chunks (id, version_id, document_id, ordinal, heading_path, content, chunk_type, embedding, is_current)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, true)`,
			c.ID, versionID, docID, c.Ordinal, c.HeadingPath, c.Content, c.ChunkType, pgvector.NewVector(c.Embedding),
		); err != nil {
			return 0, fmt.Errorf("insert chunk %s: %w", c.ID, err)
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM document_audiences WHERE document_id = $1`, docID); err != nil {
		return 0, err
	}
	if len(v.Audiences) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO audiences (id) SELECT unnest($1::text[]) ON CONFLICT DO NOTHING`, v.Audiences); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO document_audiences (document_id, audience_id)
			SELECT $1, unnest($2::text[])`, docID, v.Audiences); err != nil {
			return 0, err
		}
	}

	if _, err := tx.Exec(ctx, `UPDATE documents SET current_version_id = $2 WHERE id = $1`, docID, versionID); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit version: %w", err)
	}
	return versionID, nil
}
