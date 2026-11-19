package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// MessageRow is one persisted question/answer pair.
type MessageRow struct {
	ConversationID string
	UserSub        string
	Question       string
	Answer         string
	Outcome        string
	Model          string
	LatencyMS      int
	InputTokens    int
	OutputTokens   int
}

// RetrievedRow records one retrieved chunk for a message (used or not).
type RetrievedRow struct {
	VersionID    int64
	ChunkID      string
	Rank         int
	Score        float64
	UsedInAnswer bool
}

// CreateConversation starts a conversation owned by userSub.
func (s *Store) CreateConversation(ctx context.Context, userSub string) (string, error) {
	var id string
	err := s.Pool.QueryRow(ctx,
		`INSERT INTO conversations (user_sub) VALUES ($1) RETURNING id::text`, userSub).Scan(&id)
	return id, err
}

// ConversationOwnedBy reports whether the conversation exists and belongs to userSub.
func (s *Store) ConversationOwnedBy(ctx context.Context, convID, userSub string) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM conversations WHERE id = $1::uuid AND user_sub = $2)`,
		convID, userSub).Scan(&ok)
	return ok, err
}

// InsertMessage persists a message and its retrieved chunks in one transaction.
func (s *Store) InsertMessage(ctx context.Context, m MessageRow, retrieved []RetrievedRow) (string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO messages (conversation_id, user_sub, question, answer, outcome, model,
		                      latency_ms, input_tokens, output_tokens)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id::text`,
		m.ConversationID, m.UserSub, m.Question, m.Answer, m.Outcome, m.Model,
		m.LatencyMS, m.InputTokens, m.OutputTokens).Scan(&id); err != nil {
		return "", err
	}
	for _, r := range retrieved {
		if _, err := tx.Exec(ctx, `
			INSERT INTO message_citations (message_id, version_id, chunk_id, rank, score, used_in_answer)
			VALUES ($1::uuid, $2, $3, $4, $5, $6)`,
			id, r.VersionID, r.ChunkID, r.Rank, r.Score, r.UsedInAnswer); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return id, nil
}

// MessageOwner returns the user who asked a message, or ErrNotFound.
func (s *Store) MessageOwner(ctx context.Context, messageID string) (string, error) {
	var sub string
	err := s.Pool.QueryRow(ctx,
		`SELECT user_sub FROM messages WHERE id = $1::uuid`, messageID).Scan(&sub)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return sub, err
}

// InsertFeedback stores a thumbs up/down with an optional comment.
func (s *Store) InsertFeedback(ctx context.Context, messageID, userSub string, rating int, comment string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO feedback (message_id, user_sub, rating, comment) VALUES ($1::uuid, $2, $3, $4)`,
		messageID, userSub, rating, comment)
	return err
}
