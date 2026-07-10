// Package db is the PostgreSQL persistence layer for Quisac. It owns a pgx
// connection pool and the queries the server needs. The schema lives in the
// embedded migrations/ directory and is applied on boot by Store.Migrate.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store wraps a pgx pool and carries every data-access method.
type Store struct {
	pool *pgxpool.Pool
}

// Open creates a pool against url and verifies connectivity. The caller owns the
// returned Store and must Close it.
func Open(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("db: parse DATABASE_URL: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: open pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Ping verifies the pool can reach Postgres. Used by the readiness probe.
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// Close releases the pool. Safe to call on a nil Store.
func (s *Store) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}
