package db

import (
	"context"
	"testing"
	"time"
)

func TestJobQueueRoundtrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.UpsertUser(ctx, 7, "octocat"); err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	job := Job{
		ID:             "job-1",
		Kind:           JobFullIndex,
		Repo:           "octocat/hello",
		InstallationID: "42",
		UserID:         7,
		Payload:        []byte(`{"repo":"octocat/hello"}`),
	}
	if err := store.EnqueueJob(ctx, job); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	got, ok, err := store.ClaimJob(ctx)
	if err != nil || !ok {
		t.Fatalf("ClaimJob: ok=%v err=%v", ok, err)
	}
	if got.ID != "job-1" || got.Repo != "octocat/hello" || got.UserID != 7 || got.Attempts != 1 {
		t.Fatalf("claimed job mismatch: %+v", got)
	}

	if err := store.CompleteJob(ctx, got.ID, []byte(`{"nodes":[]}`)); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}
	res, err := store.GetIndexJob(ctx, "job-1", 7)
	if err != nil {
		t.Fatalf("GetIndexJob: %v", err)
	}
	if res.Status != StatusDone || string(res.Graph) != `{"nodes": []}` {
		t.Fatalf("unexpected result: status=%s graph=%s", res.Status, res.Graph)
	}
}

// TestClaimSerialisesPerRepo is the core guarantee: two pending jobs for the
// same repo must never both be running at once.
func TestClaimSerialisesPerRepo(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for _, id := range []string{"a", "b"} {
		if err := store.EnqueueJob(ctx, Job{ID: id, Kind: JobPush, Repo: "o/r", InstallationID: "1"}); err != nil {
			t.Fatalf("enqueue %s: %v", id, err)
		}
	}

	first, ok, err := store.ClaimJob(ctx)
	if err != nil || !ok {
		t.Fatalf("first claim: ok=%v err=%v", ok, err)
	}
	// A second claim must find nothing: the repo already has a running job.
	_, ok, err = store.ClaimJob(ctx)
	if err != nil {
		t.Fatalf("second claim err: %v", err)
	}
	if ok {
		t.Fatal("second claim returned a job for a repo that already has one running")
	}

	// Finishing the first frees the repo; the second becomes claimable.
	if err := store.CompleteJob(ctx, first.ID, nil); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, ok, err = store.ClaimJob(ctx); err != nil || !ok {
		t.Fatalf("third claim after completion: ok=%v err=%v", ok, err)
	}
}

func TestFailRetriesThenGivesUp(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.EnqueueJob(ctx, Job{ID: "j", Kind: JobPush, Repo: "o/r", InstallationID: "1", MaxAttempts: 2}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Attempt 1: claim then fail → back to pending (still has an attempt left).
	if _, ok, _ := store.ClaimJob(ctx); !ok {
		t.Fatal("claim 1")
	}
	if err := store.FailJob(ctx, "j", "boom"); err != nil {
		t.Fatalf("fail 1: %v", err)
	}
	// Attempt 2: claim then fail → attempts == max_attempts → failed for good.
	if _, ok, _ := store.ClaimJob(ctx); !ok {
		t.Fatal("claim 2 (should be pending again)")
	}
	if err := store.FailJob(ctx, "j", "boom again"); err != nil {
		t.Fatalf("fail 2: %v", err)
	}
	// No more retries.
	if _, ok, _ := store.ClaimJob(ctx); ok {
		t.Fatal("job should be terminally failed, not claimable")
	}
}

func TestReapStuckJobs(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.EnqueueJob(ctx, Job{ID: "stuck", Kind: JobPush, Repo: "o/r", InstallationID: "1"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, ok, _ := store.ClaimJob(ctx); !ok {
		t.Fatal("claim")
	}
	// Force the running job to look old.
	if _, err := store.pool.Exec(ctx, `UPDATE jobs SET started_at = now() - interval '1 hour' WHERE id = 'stuck'`); err != nil {
		t.Fatalf("age job: %v", err)
	}

	n, err := store.ReapStuckJobs(ctx, 30*time.Minute)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if n != 1 {
		t.Fatalf("reaped %d, want 1", n)
	}
	// Reaped back to pending → claimable again (blocked repo recovered).
	if _, ok, _ := store.ClaimJob(ctx); !ok {
		t.Fatal("reaped job should be claimable again")
	}
}

func TestGetIndexJobScopedToUser(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.UpsertUser(ctx, 1, "owner"); err != nil {
		t.Fatalf("user: %v", err)
	}
	if err := store.EnqueueJob(ctx, Job{ID: "priv", Kind: JobFullIndex, Repo: "o/r", InstallationID: "1", UserID: 1}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	// A different user must not be able to read it.
	if _, err := store.GetIndexJob(ctx, "priv", 999); err != ErrNotFound {
		t.Fatalf("cross-user read: got %v, want ErrNotFound", err)
	}
	if _, err := store.GetIndexJob(ctx, "priv", 1); err != nil {
		t.Fatalf("owner read: %v", err)
	}
}

func TestDeleteFinishedJobs(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.EnqueueJob(ctx, Job{ID: "old", Kind: JobPush, Repo: "o/r", InstallationID: "1"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, ok, _ := store.ClaimJob(ctx); !ok {
		t.Fatal("claim")
	}
	if err := store.CompleteJob(ctx, "old", nil); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE jobs SET finished_at = now() - interval '2 days' WHERE id = 'old'`); err != nil {
		t.Fatalf("age: %v", err)
	}

	n, err := store.DeleteFinishedJobs(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n != 1 {
		t.Fatalf("deleted %d, want 1", n)
	}
}
