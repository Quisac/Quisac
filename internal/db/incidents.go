package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Incident mirrors the incidents table row.
type Incident struct {
	ID             string
	InstallationID string
	PDNumber       int
	Title       string
	ServiceName string
	Urgency     string
	ErrorBody   string
	Status      string    // open | traversing | traversed | failed
	Repo        string    // populated after traversal
	EntryPoint  []byte    // JSON, core.Node
	Candidates  []byte    // JSON, []traverse.CandidateNode
	ReceivedAt  time.Time
	TraversedAt *time.Time
}

// CreateIncident inserts a new incident row (called when the PD webhook fires).
// Conflicts are ignored so duplicate deliveries for the same incident are safe.
func (s *Store) CreateIncident(ctx context.Context, inc Incident) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO incidents (id, installation_id, pd_number, title, service_name, urgency, error_body)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO NOTHING`,
		inc.ID, inc.InstallationID, inc.PDNumber, inc.Title, inc.ServiceName, inc.Urgency, inc.ErrorBody,
	)
	if err != nil {
		return fmt.Errorf("db: create incident: %w", err)
	}
	return nil
}

// ListIncidents returns the 50 most recent incidents for a given installation, newest first.
func (s *Store) ListIncidents(ctx context.Context, installationID string) ([]Incident, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, installation_id, COALESCE(pd_number, 0), title, service_name, urgency, COALESCE(error_body, ''),
		       status, COALESCE(repo, ''), entry_point, candidates, received_at, traversed_at
		  FROM incidents
		 WHERE installation_id = $1
		 ORDER BY received_at DESC
		 LIMIT 50`, installationID)
	if err != nil {
		return nil, fmt.Errorf("db: list incidents: %w", err)
	}
	defer rows.Close()

	var out []Incident
	for rows.Next() {
		var inc Incident
		if err := rows.Scan(
			&inc.ID, &inc.InstallationID, &inc.PDNumber, &inc.Title, &inc.ServiceName, &inc.Urgency, &inc.ErrorBody,
			&inc.Status, &inc.Repo, &inc.EntryPoint, &inc.Candidates,
			&inc.ReceivedAt, &inc.TraversedAt,
		); err != nil {
			return nil, fmt.Errorf("db: scan incident: %w", err)
		}
		out = append(out, inc)
	}
	return out, rows.Err()
}

// GetIncident fetches a single incident by id.
func (s *Store) GetIncident(ctx context.Context, id string) (Incident, error) {
	var inc Incident
	err := s.pool.QueryRow(ctx, `
		SELECT id, installation_id, COALESCE(pd_number, 0), title, service_name, urgency, COALESCE(error_body, ''),
		       status, COALESCE(repo, ''), entry_point, candidates, received_at, traversed_at
		  FROM incidents WHERE id = $1`, id,
	).Scan(
		&inc.ID, &inc.InstallationID, &inc.PDNumber, &inc.Title, &inc.ServiceName, &inc.Urgency, &inc.ErrorBody,
		&inc.Status, &inc.Repo, &inc.EntryPoint, &inc.Candidates,
		&inc.ReceivedAt, &inc.TraversedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Incident{}, ErrNotFound
		}
		return Incident{}, fmt.Errorf("db: get incident: %w", err)
	}
	return inc, nil
}

// MarkTraversing sets an incident's status to 'traversing'.
func (s *Store) MarkTraversing(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE incidents SET status = 'traversing' WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("db: mark traversing: %w", err)
	}
	return nil
}

// UpdateIncidentResult writes the BFS result back to the incident row.
func (s *Store) UpdateIncidentResult(ctx context.Context, id, repo string, entryPoint, candidates any) error {
	epJSON, err := json.Marshal(entryPoint)
	if err != nil {
		return fmt.Errorf("db: marshal entry_point: %w", err)
	}
	cJSON, err := json.Marshal(candidates)
	if err != nil {
		return fmt.Errorf("db: marshal candidates: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE incidents
		   SET status       = 'traversed',
		       repo         = $2,
		       entry_point  = $3,
		       candidates   = $4,
		       traversed_at = now()
		 WHERE id = $1`,
		id, repo, epJSON, cJSON,
	)
	if err != nil {
		return fmt.Errorf("db: update incident result: %w", err)
	}
	return nil
}

// FailIncidentTraversal marks a traversal attempt as failed.
func (s *Store) FailIncidentTraversal(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE incidents SET status = 'failed' WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("db: fail incident traversal: %w", err)
	}
	return nil
}

// ListIndexedRepos returns all distinct repos that have completed full-index
// jobs for a given installation — these are the repos we can traverse.
func (s *Store) ListIndexedRepos(ctx context.Context, installationID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT repo FROM jobs
		 WHERE installation_id = $1
		   AND kind    = 'full_index'
		   AND status  = 'done'
		 ORDER BY repo`, installationID)
	if err != nil {
		return nil, fmt.Errorf("db: list indexed repos: %w", err)
	}
	defer rows.Close()
	var repos []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, fmt.Errorf("db: scan repo: %w", err)
		}
		repos = append(repos, r)
	}
	return repos, rows.Err()
}

