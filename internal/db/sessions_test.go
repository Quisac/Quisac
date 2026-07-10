package db

import (
	"context"
	"os"
	"testing"
	"time"
)

// newTestStore connects to TEST_DATABASE_URL, applies migrations, and clears the
// tables. The test is skipped when the variable is unset, so plain
// `go test ./...` needs no database.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run db integration tests")
	}
	store, err := Open(context.Background(), url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(store.Close)

	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `TRUNCATE jobs, sessions, users CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return store
}

func TestSessionRoundtrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.UpsertUser(ctx, 42, "octocat"); err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	// Upsert must be idempotent and refresh the login.
	if err := store.UpsertUser(ctx, 42, "octocat-renamed"); err != nil {
		t.Fatalf("UpsertUser (repeat): %v", err)
	}

	want := Session{
		ID:             "tok-123",
		UserID:         42,
		InstallationID: "555",
		ExpiresAt:      time.Now().Add(time.Hour),
	}
	if err := store.CreateSession(ctx, want); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := store.GetSession(ctx, "tok-123")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.UserID != 42 || got.Login != "octocat-renamed" || got.InstallationID != "555" {
		t.Errorf("GetSession = %+v, want user 42 / octocat-renamed / 555", got)
	}
}

func TestGetSessionExpired(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.UpsertUser(ctx, 1, "u"); err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	if err := store.CreateSession(ctx, Session{
		ID: "old", UserID: 1, InstallationID: "1",
		ExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if _, err := store.GetSession(ctx, "old"); err != ErrNotFound {
		t.Errorf("expired session err = %v, want ErrNotFound", err)
	}
}

func TestDeleteSession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.UpsertUser(ctx, 1, "u"); err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	if err := store.CreateSession(ctx, Session{
		ID: "s", UserID: 1, InstallationID: "1", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := store.DeleteSession(ctx, "s"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := store.GetSession(ctx, "s"); err != ErrNotFound {
		t.Errorf("after delete err = %v, want ErrNotFound", err)
	}
	// Deleting an unknown id is a no-op, not an error.
	if err := store.DeleteSession(ctx, "ghost"); err != nil {
		t.Errorf("DeleteSession(ghost) = %v, want nil", err)
	}
}

func TestGetSessionNotFound(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.GetSession(context.Background(), "nope"); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
