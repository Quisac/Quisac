package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrNotFound is returned when a lookup matches no row.
var ErrNotFound = errors.New("db: not found")

// Session is a persisted user session. ID is the opaque token stored in the
// cookie; Login is denormalised from the users table on read.
type Session struct {
	ID             string
	UserID         int64
	Login          string
	InstallationID string
	ExpiresAt      time.Time
}

// UpsertUser records (or refreshes) a GitHub user, keyed by their numeric id.
func (s *Store) UpsertUser(ctx context.Context, id int64, login string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO users (id, login)
		VALUES ($1, $2)
		ON CONFLICT (id) DO UPDATE SET login = EXCLUDED.login`,
		id, login,
	)
	if err != nil {
		return fmt.Errorf("db: upsert user %d: %w", id, err)
	}
	return nil
}

// CreateSession inserts a new session row.
func (s *Store) CreateSession(ctx context.Context, sess Session) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions (id, user_id, installation_id, expires_at)
		VALUES ($1, $2, $3, $4)`,
		sess.ID, sess.UserID, sess.InstallationID, sess.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("db: create session: %w", err)
	}
	return nil
}

// GetSession returns a live session by id, joined to the user's login. A missing
// or expired session returns ErrNotFound.
func (s *Store) GetSession(ctx context.Context, id string) (Session, error) {
	var sess Session
	err := s.pool.QueryRow(ctx, `
		SELECT s.id, s.user_id, u.login, s.installation_id, s.expires_at
		  FROM sessions s
		  JOIN users u ON u.id = s.user_id
		 WHERE s.id = $1 AND s.expires_at > now()`,
		id,
	).Scan(&sess.ID, &sess.UserID, &sess.Login, &sess.InstallationID, &sess.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, fmt.Errorf("db: get session: %w", err)
	}
	return sess, nil
}

// DeleteSession removes a session (logout). Deleting an unknown id is not an error.
func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("db: delete session: %w", err)
	}
	return nil
}
