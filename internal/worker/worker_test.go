package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"quisac/internal/core"
	"quisac/internal/db"
)

// fakeQueue is an in-memory stand-in for the durable queue: enough to drive one
// job through the pool and observe the recorded outcome.
type fakeQueue struct {
	mu       sync.Mutex
	pending  []db.Job
	done     map[string][]byte
	failed   map[string]string
	claimAll chan struct{} // closed when the queue empties
	once     sync.Once
}

func newFakeQueue(jobs ...db.Job) *fakeQueue {
	return &fakeQueue{
		pending:  jobs,
		done:     map[string][]byte{},
		failed:   map[string]string{},
		claimAll: make(chan struct{}),
	}
}

func (q *fakeQueue) ClaimJob(context.Context) (db.Job, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 {
		q.once.Do(func() { close(q.claimAll) })
		return db.Job{}, false, nil
	}
	j := q.pending[0]
	q.pending = q.pending[1:]
	j.Attempts++
	return j, true, nil
}

func (q *fakeQueue) CompleteJob(_ context.Context, id string, graph []byte) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.done[id] = graph
	return nil
}

func (q *fakeQueue) FailJob(_ context.Context, id, msg string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.failed[id] = msg
	return nil
}

func (q *fakeQueue) ReapStuckJobs(context.Context, time.Duration) (int64, error)      { return 0, nil }
func (q *fakeQueue) DeleteFinishedJobs(context.Context, time.Duration) (int64, error) { return 0, nil }

// fakeIndexer records calls and returns canned results.
type fakeIndexer struct {
	graph     *core.Graph
	indexErr  error
	pushErr   error
	pushCalls int
	mu        sync.Mutex
}

func (ix *fakeIndexer) FullIndex(context.Context, string, string, string) (*core.Graph, error) {
	if ix.indexErr != nil {
		return nil, ix.indexErr
	}
	return ix.graph, nil
}

func (ix *fakeIndexer) ProcessPush(context.Context, string, string, string, string, []string, bool) error {
	ix.mu.Lock()
	ix.pushCalls++
	ix.mu.Unlock()
	return ix.pushErr
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// runUntilDrained runs the pool until the fake queue reports empty, then cancels.
func runUntilDrained(t *testing.T, q *fakeQueue, ix Indexer) *Pool {
	t.Helper()
	p := New(q, ix, quietLogger(), Config{Workers: 1, PollInterval: 5 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()

	select {
	case <-q.claimAll:
	case <-time.After(2 * time.Second):
		t.Fatal("pool did not drain the queue in time")
	}
	// Give the worker a moment to record the last outcome, then stop.
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pool did not stop after cancel")
	}
	return p
}

func TestProcessFullIndex(t *testing.T) {
	g := core.NewGraph()
	q := newFakeQueue(db.Job{ID: "idx", Kind: db.JobFullIndex, Repo: "o/r", InstallationID: "1"})
	ix := &fakeIndexer{graph: g}

	runUntilDrained(t, q, ix)

	want, _ := g.ToJSON()
	if got, ok := q.done["idx"]; !ok || string(got) != want {
		t.Fatalf("full-index result: ok=%v got=%s want=%s", ok, got, want)
	}
	if _, failed := q.failed["idx"]; failed {
		t.Fatal("full-index job should not have failed")
	}
}

func TestProcessPush(t *testing.T) {
	payload, _ := json.Marshal(PushPayload{Before: "a", After: "b", Changed: []string{"x.go"}, Complete: true})
	q := newFakeQueue(db.Job{ID: "push", Kind: db.JobPush, Repo: "o/r", InstallationID: "1", Payload: payload})
	ix := &fakeIndexer{}

	runUntilDrained(t, q, ix)

	if ix.pushCalls != 1 {
		t.Fatalf("ProcessPush called %d times, want 1", ix.pushCalls)
	}
	if _, ok := q.done["push"]; !ok {
		t.Fatal("push job should be marked done")
	}
}

func TestProcessRecordsFailure(t *testing.T) {
	q := newFakeQueue(db.Job{ID: "bad", Kind: db.JobFullIndex, Repo: "o/r", InstallationID: "1"})
	ix := &fakeIndexer{indexErr: errors.New("boom")}

	runUntilDrained(t, q, ix)

	if msg, ok := q.failed["bad"]; !ok || msg != "boom" {
		t.Fatalf("expected failure recorded, got ok=%v msg=%q", ok, msg)
	}
	if _, ok := q.done["bad"]; ok {
		t.Fatal("failed job should not be marked done")
	}
}
