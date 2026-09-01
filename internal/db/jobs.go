package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// JobKind distinguishes the kinds of async work on the queue.
type JobKind string

const (
	JobFullIndex JobKind = "full_index" // user-initiated; result stored in graph
	JobPush      JobKind = "push"       // webhook-driven incremental update
	JobResolve   JobKind = "resolve"    // alert-driven incident resolution
)

// ResolveRepoKey is the synthetic repo key a resolve job is filed under. The
// queue runs one job per repo, which is right for indexing but would make an
// alert queue behind a full index of the repo it is about to traverse. A
// per-incident key serialises retries of itself and leaves the index lane free.
func ResolveRepoKey(incidentID string) string { return "incident:" + incidentID }

// JobStatus is a job's lifecycle state.
type JobStatus string

const (
	StatusPending JobStatus = "pending"
	StatusRunning JobStatus = "running"
	StatusDone    JobStatus = "done"
	StatusFailed  JobStatus = "failed"
)

// Job is a unit of async work a worker claims and processes.
type Job struct {
	ID             string
	Kind           JobKind
	Repo           string
	InstallationID string
	UserID         int64  // 0 for webhook pushes (no associated user)
	Payload        []byte // JSON, kind-specific input
	Attempts       int
	MaxAttempts    int
}

// IndexJobResult is the pollable state of a full-index job.
type IndexJobResult struct {
	Status JobStatus
	Graph  []byte // populated when Status == StatusDone
	Error  string // populated when Status == StatusFailed
}

// EnqueueJob inserts a pending job. The caller supplies the id so it can be
// returned to the client for polling before the worker runs.
func (s *Store) EnqueueJob(ctx context.Context, j Job) error {
	var userID *int64
	if j.UserID != 0 {
		userID = &j.UserID
	}
	maxAttempts := j.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = 5
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO jobs (id, kind, repo, installation_id, user_id, payload, max_attempts)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		j.ID, j.Kind, j.Repo, j.InstallationID, userID, j.Payload, maxAttempts,
	)
	if err != nil {
		return fmt.Errorf("db: enqueue job: %w", err)
	}
	return nil
}

// ClaimJob atomically claims the oldest pending job whose repo has none running,
// returning ok=false when nothing is eligible.
//
// Serialisation comes from the jobs_one_running_per_repo partial unique index:
// the loser of a race hits a unique violation, which counts as "not claimed".
func (s *Store) ClaimJob(ctx context.Context) (Job, bool, error) {
	const claim = `
		UPDATE jobs SET status = 'running', started_at = now(), attempts = attempts + 1
		WHERE id = (
			SELECT j.id FROM jobs j
			WHERE j.status = 'pending'
			  AND NOT EXISTS (
			      SELECT 1 FROM jobs r WHERE r.repo = j.repo AND r.status = 'running'
			  )
			ORDER BY j.created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING id, kind, repo, installation_id, user_id, payload, attempts, max_attempts`

	for attempt := 0; attempt < 3; attempt++ {
		var (
			j      Job
			userID *int64
		)
		err := s.pool.QueryRow(ctx, claim).Scan(
			&j.ID, &j.Kind, &j.Repo, &j.InstallationID, &userID, &j.Payload, &j.Attempts, &j.MaxAttempts,
		)
		if err == nil {
			if userID != nil {
				j.UserID = *userID
			}
			return j, true, nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return Job{}, false, nil // nothing eligible
		}
		if isUniqueViolation(err) {
			continue // lost the per-repo race; try the next eligible job
		}
		return Job{}, false, fmt.Errorf("db: claim job: %w", err)
	}
	return Job{}, false, nil
}

// CompleteJob marks a job done, storing an optional result graph.
func (s *Store) CompleteJob(ctx context.Context, id string, graph []byte) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE jobs
		   SET status = 'done', graph = $2, error = NULL, finished_at = now()
		 WHERE id = $1`,
		id, graph,
	)
	if err != nil {
		return fmt.Errorf("db: complete job: %w", err)
	}
	return nil
}

// FailJob returns a job to pending while it has attempts left, otherwise marks
// it failed. Returning to pending also releases the per-repo running slot.
func (s *Store) FailJob(ctx context.Context, id, errMsg string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE jobs
		   SET status      = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'pending' END,
		       error       = $2,
		       finished_at = CASE WHEN attempts >= max_attempts THEN now() ELSE NULL END
		 WHERE id = $1`,
		id, errMsg,
	)
	if err != nil {
		return fmt.Errorf("db: fail job: %w", err)
	}
	return nil
}

// GetIndexJob returns a job's pollable state, scoped to its owner.
func (s *Store) GetIndexJob(ctx context.Context, id string, userID int64) (IndexJobResult, error) {
	var (
		res   IndexJobResult
		graph []byte
		errTx *string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT status, graph, error
		  FROM jobs
		 WHERE id = $1 AND user_id = $2 AND kind = 'full_index'`,
		id, userID,
	).Scan(&res.Status, &graph, &errTx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return IndexJobResult{}, ErrNotFound
		}
		return IndexJobResult{}, fmt.Errorf("db: get index job: %w", err)
	}
	res.Graph = graph
	if errTx != nil {
		res.Error = *errTx
	}
	return res, nil
}

// CountActiveIndexJobs counts a user's pending or running full-index jobs.
func (s *Store) CountActiveIndexJobs(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM jobs
		 WHERE user_id = $1 AND kind = 'full_index' AND status IN ('pending', 'running')`,
		userID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("db: count active index jobs: %w", err)
	}
	return n, nil
}

// ReapStuckJobs recovers jobs whose worker died mid-run, returning any running
// longer than olderThan to pending. olderThan must exceed the longest legitimate
// job so live work is never reaped.
func (s *Store) ReapStuckJobs(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE jobs
		   SET status      = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'pending' END,
		       error       = 'reaped: worker did not finish in time',
		       finished_at = CASE WHEN attempts >= max_attempts THEN now() ELSE NULL END
		 WHERE status = 'running' AND started_at < now() - $1::interval`,
		olderThan.String(),
	)
	if err != nil {
		return 0, fmt.Errorf("db: reap stuck jobs: %w", err)
	}
	return tag.RowsAffected(), nil
}

// DeleteFinishedJobs evicts terminal jobs older than olderThan, bounding table
// and result-graph storage.
func (s *Store) DeleteFinishedJobs(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM jobs
		 WHERE status IN ('done', 'failed') AND finished_at < now() - $1::interval`,
		olderThan.String(),
	)
	if err != nil {
		return 0, fmt.Errorf("db: delete finished jobs: %w", err)
	}
	return tag.RowsAffected(), nil
}

// isUniqueViolation reports whether err is a Postgres unique-constraint error.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
