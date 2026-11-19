package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// JobStatus is the admin view of one ingestion job.
type JobStatus struct {
	ID         int64
	SourcePath string
	Status     string
	Attempts   int
	LastError  string
}

// GetJob returns one ingestion job, or ErrNotFound.
func (s *Store) GetJob(ctx context.Context, id int64) (JobStatus, error) {
	var j JobStatus
	err := s.Pool.QueryRow(ctx,
		`SELECT id, source_path, status, attempts, last_error FROM ingestion_jobs WHERE id = $1`, id).
		Scan(&j.ID, &j.SourcePath, &j.Status, &j.Attempts, &j.LastError)
	if errors.Is(err, pgx.ErrNoRows) {
		return JobStatus{}, ErrNotFound
	}
	return j, err
}

// OutcomeSummary counts messages per outcome. Every known outcome appears, even at zero.
func (s *Store) OutcomeSummary(ctx context.Context) (map[string]int, error) {
	out := map[string]int{
		"answered": 0, "declined_no_source": 0, "routed_to_human": 0, "clarification_needed": 0,
	}
	rows, err := s.Pool.Query(ctx, `SELECT outcome, count(*) FROM messages GROUP BY outcome`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var outcome string
		var n int
		if err := rows.Scan(&outcome, &n); err != nil {
			return nil, err
		}
		out[outcome] = n
	}
	return out, rows.Err()
}

// QuestionCount is one distinct unanswered question and how often it was asked.
// It carries no user identity: the handbook-gap report is aggregated.
type QuestionCount struct {
	Question string
	Count    int
}

// UnansweredQuestions lists the most frequent questions that were not answered from the handbook.
// limit is clamped to 1..100.
func (s *Store) UnansweredQuestions(ctx context.Context, limit int) ([]QuestionCount, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT question, count(*) AS n
		FROM messages
		WHERE outcome <> 'answered'
		GROUP BY question
		ORDER BY n DESC, question
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QuestionCount
	for rows.Next() {
		var q QuestionCount
		if err := rows.Scan(&q.Question, &q.Count); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}
