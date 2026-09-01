package db

import (
	"context"
	"fmt"
	"time"

	"quisac/internal/core"
)

// FileStats returns every recorded file's history for a repo, keyed by
// repo-relative path. It satisfies traverse.StatsSource.
func (s *Store) FileStats(ctx context.Context, repo string) (map[string]core.FileStat, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT path, change_count, last_changed_at, fault_count, last_fault_at
		  FROM file_stats
		 WHERE repo = $1`, repo)
	if err != nil {
		return nil, fmt.Errorf("db: file stats: %w", err)
	}
	defer rows.Close()

	out := make(map[string]core.FileStat)
	for rows.Next() {
		var (
			path        string
			stat        core.FileStat
			lastChanged *time.Time
			lastFault   *time.Time
		)
		if err := rows.Scan(&path, &stat.ChangeCount, &lastChanged, &stat.FaultCount, &lastFault); err != nil {
			return nil, fmt.Errorf("db: scan file stat: %w", err)
		}
		if lastChanged != nil {
			stat.LastChangedAt = *lastChanged
		}
		if lastFault != nil {
			stat.LastFaultAt = *lastFault
		}
		out[path] = stat
	}
	return out, rows.Err()
}

// RecordFileChanges increments churn for the paths a push touched.
func (s *Store) RecordFileChanges(ctx context.Context, repo string, paths []string, at time.Time) error {
	if len(paths) == 0 {
		return nil
	}
	if at.IsZero() {
		at = time.Now()
	}
	// unnest turns the path slice into rows, so a large push is one round trip.
	_, err := s.pool.Exec(ctx, `
		INSERT INTO file_stats (repo, path, change_count, last_changed_at)
		SELECT $1, p, 1, $3 FROM unnest($2::text[]) AS p
		ON CONFLICT (repo, path) DO UPDATE
		   SET change_count    = file_stats.change_count + 1,
		       last_changed_at = GREATEST(file_stats.last_changed_at, EXCLUDED.last_changed_at)`,
		repo, paths, at)
	if err != nil {
		return fmt.Errorf("db: record file changes: %w", err)
	}
	return nil
}

// SeedFileChurn installs churn read from commit history at full-index time. It
// overwrites rather than accumulating, so re-indexing does not multiply counts.
// Fault history is untouched: that is earned from incidents, not commits.
func (s *Store) SeedFileChurn(ctx context.Context, repo string, churn []core.FileChurn) error {
	if len(churn) == 0 {
		return nil
	}
	paths := make([]string, len(churn))
	changes := make([]int32, len(churn))
	when := make([]time.Time, len(churn))
	for i, c := range churn {
		paths[i] = c.Path
		changes[i] = int32(c.Changes)
		when[i] = c.LastChangedAt
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO file_stats (repo, path, change_count, last_changed_at)
		SELECT $1, p, c, t
		  FROM unnest($2::text[], $3::int[], $4::timestamptz[]) AS s(p, c, t)
		ON CONFLICT (repo, path) DO UPDATE
		   SET change_count    = EXCLUDED.change_count,
		       last_changed_at = EXCLUDED.last_changed_at`,
		repo, paths, changes, when)
	if err != nil {
		return fmt.Errorf("db: seed file churn: %w", err)
	}
	return nil
}

// RecordFaults marks files as implicated, raising them as suspects in future
// traversals. Called only when a resolution ships a fix: a candidate that ranked
// highly and turned out innocent must not accrue a fault.
func (s *Store) RecordFaults(ctx context.Context, repo string, paths []string, at time.Time) error {
	if len(paths) == 0 {
		return nil
	}
	if at.IsZero() {
		at = time.Now()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO file_stats (repo, path, fault_count, last_fault_at)
		SELECT $1, p, 1, $3 FROM unnest($2::text[]) AS p
		ON CONFLICT (repo, path) DO UPDATE
		   SET fault_count   = file_stats.fault_count + 1,
		       last_fault_at = GREATEST(file_stats.last_fault_at, EXCLUDED.last_fault_at)`,
		repo, paths, at)
	if err != nil {
		return fmt.Errorf("db: record faults: %w", err)
	}
	return nil
}
