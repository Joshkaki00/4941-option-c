package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Job is one claimed ingestion job.
type Job struct {
	ID         int64
	SourcePath string
	Attempts   int
}

// EnqueueIngestion adds a job for one source file.
func (s *Store) EnqueueIngestion(ctx context.Context, sourcePath string) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx,
		`INSERT INTO ingestion_jobs (source_path) VALUES ($1) RETURNING id`, sourcePath).Scan(&id)
	return id, err
}

// staleLockAfter is how long a "running" job may sit before another worker reclaims it
// (covers a process that crashed mid-job).
const staleLockAfter = "10 minutes"

// ClaimJob takes one runnable job with FOR UPDATE SKIP LOCKED.
// It returns (nil, nil) when the queue is empty.
func (s *Store) ClaimJob(ctx context.Context) (*Job, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var j Job
	err = tx.QueryRow(ctx, `
		SELECT id, source_path, attempts FROM ingestion_jobs
		WHERE (status = 'queued' AND run_after <= now())
		   OR (status = 'running' AND locked_at < now() - $1::interval)
		ORDER BY id
		FOR UPDATE SKIP LOCKED
		LIMIT 1`, staleLockAfter).Scan(&j.ID, &j.SourcePath, &j.Attempts)
	if errorsIsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE ingestion_jobs SET status = 'running', attempts = attempts + 1, locked_at = now()
		WHERE id = $1`, j.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	j.Attempts++
	return &j, nil
}

// FinishJob records success, or retries with backoff until maxAttempts, then fails.
func (s *Store) FinishJob(ctx context.Context, j Job, runErr error, maxAttempts int) error {
	if runErr == nil {
		_, err := s.Pool.Exec(ctx,
			`UPDATE ingestion_jobs SET status = 'done', last_error = '' WHERE id = $1`, j.ID)
		return err
	}
	status := "queued"
	if j.Attempts >= maxAttempts {
		status = "failed"
	}
	backoff := time.Duration(1<<min(j.Attempts, 6)) * time.Second
	_, err := s.Pool.Exec(ctx, `
		UPDATE ingestion_jobs SET status = $2, last_error = $3,
		       run_after = now() + make_interval(secs => $4), locked_at = NULL
		WHERE id = $1`, j.ID, status, runErr.Error(), backoff.Seconds())
	return err
}

// errorsIsNoRows reports whether err is pgx's no-rows error.
func errorsIsNoRows(err error) bool { return err == pgx.ErrNoRows }
