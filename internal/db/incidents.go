package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Incident mirrors the incidents table row. Status tracks the graph traversal
// and Resolution the fix pipeline: they are separate because a pattern-cache hit
// resolves an incident without touching the graph at all.
type Incident struct {
	ID             string
	InstallationID string
	PDNumber       int
	Title          string
	ServiceName    string
	Urgency        string
	ErrorBody      string
	Status         string // open | traversing | traversed | failed
	Repo           string // populated once a repo is identified
	EntryPoint     []byte // JSON, core.Node
	Candidates     []byte // JSON, []traverse.CandidateNode
	ReceivedAt     time.Time
	TraversedAt    *time.Time

	Fingerprint string // normalised error shape; the pattern-cache key
	Resolution  Resolution
}

// Resolution is the outcome of the fix pipeline for one incident.
type Resolution struct {
	Status     string  // pending | resolving | resolved | no_fix | failed
	Method     string  // pattern | llm | "" when unresolved
	PatternID  int64   // the pattern reused, when Method == "pattern"
	RootCause  string  // the diagnosis, whether or not a fix was produced
	Confidence float64 // 0-1
	Summary    string  // one-line description of the fix
	Files      []byte  // JSON, []patch.FileChange
	PRURL      string
	PRNumber   int
	Branch     string

	// LLM spend; both stay zero for a pattern-cache resolution.
	InputTokens  int
	OutputTokens int

	// Error explains a failure; NoFixReason a decline — the model diagnosed the
	// incident but would not guess at a patch.
	Error      string
	ResolvedAt *time.Time
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

// incidentColumns is shared by the list and single-row reads, so they cannot drift.
const incidentColumns = `id, installation_id, COALESCE(pd_number, 0), title, service_name,
	urgency, COALESCE(error_body, ''), status, COALESCE(repo, ''), entry_point, candidates,
	received_at, traversed_at, COALESCE(fingerprint, ''), resolution_status,
	COALESCE(resolution_method, ''), COALESCE(pattern_id, 0), COALESCE(root_cause, ''),
	COALESCE(confidence, 0), COALESCE(fix_summary, ''), fix_files, COALESCE(pr_url, ''),
	COALESCE(pr_number, 0), COALESCE(branch, ''), input_tokens, output_tokens,
	COALESCE(resolution_error, ''), resolved_at`

func scanIncident(row scanner) (Incident, error) {
	var inc Incident
	err := row.Scan(
		&inc.ID, &inc.InstallationID, &inc.PDNumber, &inc.Title, &inc.ServiceName, &inc.Urgency,
		&inc.ErrorBody, &inc.Status, &inc.Repo, &inc.EntryPoint, &inc.Candidates,
		&inc.ReceivedAt, &inc.TraversedAt, &inc.Fingerprint,
		&inc.Resolution.Status, &inc.Resolution.Method, &inc.Resolution.PatternID,
		&inc.Resolution.RootCause, &inc.Resolution.Confidence, &inc.Resolution.Summary,
		&inc.Resolution.Files, &inc.Resolution.PRURL, &inc.Resolution.PRNumber,
		&inc.Resolution.Branch, &inc.Resolution.InputTokens, &inc.Resolution.OutputTokens,
		&inc.Resolution.Error, &inc.Resolution.ResolvedAt,
	)
	return inc, err
}

// ListIncidents returns the 50 most recent incidents for a given installation, newest first.
func (s *Store) ListIncidents(ctx context.Context, installationID string) ([]Incident, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+incidentColumns+`
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
		inc, err := scanIncident(rows)
		if err != nil {
			return nil, fmt.Errorf("db: scan incident: %w", err)
		}
		out = append(out, inc)
	}
	return out, rows.Err()
}

// GetIncident fetches a single incident by id.
func (s *Store) GetIncident(ctx context.Context, id string) (Incident, error) {
	inc, err := scanIncident(s.pool.QueryRow(ctx,
		`SELECT `+incidentColumns+` FROM incidents WHERE id = $1`, id))
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

// SetFingerprint stores the normalised error shape as soon as the incident is
// analysed, so a failed resolution still leaves the key a later match can use.
func (s *Store) SetFingerprint(ctx context.Context, id, fingerprint string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE incidents SET fingerprint = $2 WHERE id = $1`, id, fingerprint)
	if err != nil {
		return fmt.Errorf("db: set fingerprint: %w", err)
	}
	return nil
}

// MarkResolving moves an incident into the fix pipeline, clearing any error left
// by a previous attempt so a retry does not show a stale failure.
func (s *Store) MarkResolving(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE incidents
		   SET resolution_status = 'resolving', resolution_error = NULL
		 WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("db: mark resolving: %w", err)
	}
	return nil
}

// SaveResolution writes the outcome of the fix pipeline, for every terminal
// outcome and not just success: a 'no_fix' still carries a root cause worth
// showing, and recording it stops the analysis re-running on the next poll.
func (s *Store) SaveResolution(ctx context.Context, id string, r Resolution) error {
	var patternID *int64
	if r.PatternID != 0 {
		patternID = &r.PatternID
	}
	var method *string
	if r.Method != "" {
		method = &r.Method
	}
	resolvedAt := "now()"
	if r.Status == "resolving" || r.Status == "pending" {
		resolvedAt = "NULL"
	}

	_, err := s.pool.Exec(ctx, `
		UPDATE incidents
		   SET resolution_status = $2,
		       resolution_method = $3,
		       pattern_id        = $4,
		       root_cause        = $5,
		       confidence        = $6,
		       fix_summary       = $7,
		       fix_files         = $8,
		       pr_url            = NULLIF($9, ''),
		       pr_number         = NULLIF($10, 0),
		       branch            = NULLIF($11, ''),
		       input_tokens      = $12,
		       output_tokens     = $13,
		       resolution_error  = NULLIF($14, ''),
		       resolved_at       = `+resolvedAt+`
		 WHERE id = $1`,
		id, r.Status, method, patternID, r.RootCause, r.Confidence, r.Summary,
		r.Files, r.PRURL, r.PRNumber, r.Branch, r.InputTokens, r.OutputTokens, r.Error,
	)
	if err != nil {
		return fmt.Errorf("db: save resolution: %w", err)
	}
	return nil
}

// SetIncidentRepo is written separately from UpdateIncidentResult because a
// pattern-cache hit knows the repo without running a traversal.
func (s *Store) SetIncidentRepo(ctx context.Context, id, repo string) error {
	_, err := s.pool.Exec(ctx, `UPDATE incidents SET repo = $2 WHERE id = $1`, id, repo)
	if err != nil {
		return fmt.Errorf("db: set incident repo: %w", err)
	}
	return nil
}

// DeleteIncident removes an incident an engineer marked resolved. It is scoped
// by installation so a session deletes only its own rows, and leaves
// incident_patterns alone: clearing the feed must not cost the cache what it knows.
func (s *Store) DeleteIncident(ctx context.Context, id, installationID string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM incidents WHERE id = $1 AND installation_id = $2`, id, installationID)
	if err != nil {
		return fmt.Errorf("db: delete incident: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
