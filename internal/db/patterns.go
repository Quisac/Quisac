package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Pattern is a fix that has worked before, stored against the normalised shape
// of the error it fixed.
type Pattern struct {
	ID             int64
	InstallationID string
	Repo           string
	Fingerprint    string   // exact-match key
	Signature      string   // the normalised signature, human-readable
	Tokens         []string // bag of words for fuzzy matching
	ServiceName    string
	RootCause      string
	FixSummary     string
	Edits          []byte // JSON []patch.Edit
	TimesSeen      int
	TimesApplied   int
	CreatedAt      time.Time
	LastSeenAt     time.Time
}

const patternColumns = `id, installation_id, repo, fingerprint, signature, tokens,
	service_name, root_cause, fix_summary, edits, times_seen, times_applied,
	created_at, last_seen_at`

// FindPattern looks up a pattern by exact fingerprint. Patterns are scoped to
// the installation, never shared across tenants — one customer's fix is their code.
func (s *Store) FindPattern(ctx context.Context, installationID, fingerprint string) (Pattern, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+patternColumns+`
		  FROM incident_patterns
		 WHERE installation_id = $1 AND fingerprint = $2
		 ORDER BY times_applied DESC, last_seen_at DESC
		 LIMIT 1`, installationID, fingerprint)

	p, err := scanPattern(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Pattern{}, ErrNotFound
		}
		return Pattern{}, fmt.Errorf("db: find pattern: %w", err)
	}
	return p, nil
}

// ListPatterns returns the most recently seen patterns for the fuzzy match that
// runs when no fingerprint matched. The limit keeps that scan bounded.
func (s *Store) ListPatterns(ctx context.Context, installationID string, limit int) ([]Pattern, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+patternColumns+`
		  FROM incident_patterns
		 WHERE installation_id = $1
		 ORDER BY last_seen_at DESC
		 LIMIT $2`, installationID, limit)
	if err != nil {
		return nil, fmt.Errorf("db: list patterns: %w", err)
	}
	defer rows.Close()

	var out []Pattern
	for rows.Next() {
		p, err := scanPattern(rows)
		if err != nil {
			return nil, fmt.Errorf("db: scan pattern: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SavePattern records a fix against an error shape, returning its id. A repeat
// fingerprint bumps times_seen and refreshes the fix, preserving fault history
// and application counts.
func (s *Store) SavePattern(ctx context.Context, p Pattern) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO incident_patterns
		    (installation_id, repo, fingerprint, signature, tokens, service_name,
		     root_cause, fix_summary, edits, times_seen, times_applied, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 1, 0, now())
		ON CONFLICT (installation_id, repo, fingerprint) DO UPDATE
		   SET signature    = EXCLUDED.signature,
		       tokens       = EXCLUDED.tokens,
		       service_name = EXCLUDED.service_name,
		       root_cause   = EXCLUDED.root_cause,
		       fix_summary  = EXCLUDED.fix_summary,
		       edits        = EXCLUDED.edits,
		       times_seen   = incident_patterns.times_seen + 1,
		       last_seen_at = now()
		RETURNING id`,
		p.InstallationID, p.Repo, p.Fingerprint, p.Signature, p.Tokens, p.ServiceName,
		p.RootCause, p.FixSummary, p.Edits,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("db: save pattern: %w", err)
	}
	return id, nil
}

// MarkPatternApplied records a reuse, which is what makes a proven pattern win
// over a merely-recorded one when several share a fingerprint.
func (s *Store) MarkPatternApplied(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE incident_patterns
		   SET times_applied = times_applied + 1, last_seen_at = now()
		 WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("db: mark pattern applied: %w", err)
	}
	return nil
}

// scanner is the shared shape of pgx.Row and pgx.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanPattern(row scanner) (Pattern, error) {
	var p Pattern
	err := row.Scan(
		&p.ID, &p.InstallationID, &p.Repo, &p.Fingerprint, &p.Signature, &p.Tokens,
		&p.ServiceName, &p.RootCause, &p.FixSummary, &p.Edits, &p.TimesSeen,
		&p.TimesApplied, &p.CreatedAt, &p.LastSeenAt,
	)
	return p, err
}
