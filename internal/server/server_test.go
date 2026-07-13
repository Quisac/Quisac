package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"quisac/internal/config"
	"quisac/internal/db"
)

// --- fakes -----------------------------------------------------------------

type fakeStore struct {
	sess    db.Session
	hasSess bool
}

func (f *fakeStore) UpsertUser(context.Context, int64, string) error { return nil }
func (f *fakeStore) CreateSession(context.Context, db.Session) error { return nil }
func (f *fakeStore) DeleteSession(context.Context, string) error     { return nil }
func (f *fakeStore) GetSession(_ context.Context, id string) (db.Session, error) {
	if f.hasSess && id == f.sess.ID {
		return f.sess, nil
	}
	return db.Session{}, db.ErrNotFound
}

type fakeQueue struct {
	active    int
	enqueued  []db.Job
	result    db.IndexJobResult
	getErr    error
	gotUserID int64
}

func (q *fakeQueue) EnqueueJob(_ context.Context, j db.Job) error {
	q.enqueued = append(q.enqueued, j)
	return nil
}
func (q *fakeQueue) GetIndexJob(_ context.Context, _ string, userID int64) (db.IndexJobResult, error) {
	q.gotUserID = userID
	if q.getErr != nil {
		return db.IndexJobResult{}, q.getErr
	}
	return q.result, nil
}
func (q *fakeQueue) CountActiveIndexJobs(context.Context, int64) (int, error) {
	return q.active, nil
}

type fakeWaker struct{ woke int }

func (w *fakeWaker) Wake() { w.woke++ }

// --- harness ---------------------------------------------------------------

const testToken = "sess-token"

func newTestServer(store SessionStore, queue JobQueue, waker Waker) *Server {
	cfg := &config.Config{}
	cfg.Server.SessionSecret = "test-secret"
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(cfg, log, store, queue, nil, waker, nil, nil)
}

func authedStore(userID int64) *fakeStore {
	return &fakeStore{hasSess: true, sess: db.Session{ID: testToken, UserID: userID, InstallationID: "77", Login: "octo"}}
}

func withSession(r *http.Request) {
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: testToken})
}

// --- tests -----------------------------------------------------------------

func TestHandleIndexEnqueues(t *testing.T) {
	q := &fakeQueue{}
	w := &fakeWaker{}
	srv := newTestServer(authedStore(101), q, w)

	req := httptest.NewRequest(http.MethodPost, "/api/index", strings.NewReader(`{"repo":"octo/hello"}`))
	withSession(req)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body)
	}
	if len(q.enqueued) != 1 {
		t.Fatalf("enqueued %d jobs, want 1", len(q.enqueued))
	}
	job := q.enqueued[0]
	if job.Kind != db.JobFullIndex || job.Repo != "octo/hello" || job.UserID != 101 || job.InstallationID != "77" {
		t.Fatalf("unexpected job: %+v", job)
	}
	if w.woke != 1 {
		t.Fatalf("waker woke %d times, want 1", w.woke)
	}
	if !strings.Contains(rec.Body.String(), `"jobId"`) {
		t.Fatalf("response missing jobId: %s", rec.Body)
	}
}

func TestHandleIndexRateLimited(t *testing.T) {
	q := &fakeQueue{active: maxActiveIndexJobs}
	srv := newTestServer(authedStore(1), q, &fakeWaker{})

	req := httptest.NewRequest(http.MethodPost, "/api/index", strings.NewReader(`{"repo":"o/r"}`))
	withSession(req)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if len(q.enqueued) != 0 {
		t.Fatal("rate-limited request should not enqueue")
	}
}

func TestHandleIndexRequiresSession(t *testing.T) {
	srv := newTestServer(&fakeStore{}, &fakeQueue{}, &fakeWaker{})
	req := httptest.NewRequest(http.MethodPost, "/api/index", strings.NewReader(`{"repo":"o/r"}`))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestHandleGraphScopesToUser(t *testing.T) {
	q := &fakeQueue{result: db.IndexJobResult{Status: db.StatusDone, Graph: []byte(`{"nodes":[]}`)}}
	srv := newTestServer(authedStore(555), q, &fakeWaker{})

	req := httptest.NewRequest(http.MethodGet, "/api/graph/job-1", nil)
	withSession(req)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if q.gotUserID != 555 {
		t.Fatalf("GetIndexJob got userID %d, want 555 (job must be scoped to caller)", q.gotUserID)
	}
	if !strings.Contains(rec.Body.String(), `"status":"done"`) {
		t.Fatalf("unexpected body: %s", rec.Body)
	}
}

func TestHandleGraphHidesInternalError(t *testing.T) {
	q := &fakeQueue{result: db.IndexJobResult{Status: db.StatusFailed, Error: "/srv/secret/path: connection refused"}}
	srv := newTestServer(authedStore(1), q, &fakeWaker{})

	req := httptest.NewRequest(http.MethodGet, "/api/graph/job-1", nil)
	withSession(req)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("response leaked internal error detail: %s", rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "indexing failed") {
		t.Fatalf("expected generic error message, got: %s", rec.Body)
	}
}

func TestHandleGraphNotFound(t *testing.T) {
	q := &fakeQueue{getErr: db.ErrNotFound}
	srv := newTestServer(authedStore(1), q, &fakeWaker{})

	req := httptest.NewRequest(http.MethodGet, "/api/graph/missing", nil)
	withSession(req)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHealthz(t *testing.T) {
	srv := newTestServer(&fakeStore{}, &fakeQueue{}, &fakeWaker{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", rec.Code)
	}
}

func TestReadyzReflectsChecks(t *testing.T) {
	srv := newTestServer(&fakeStore{}, &fakeQueue{}, &fakeWaker{})
	srv.AddReadinessCheck("ok-dep", func(context.Context) error { return nil })
	srv.AddReadinessCheck("bad-dep", func(context.Context) error { return errors.New("down") })

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz with a failing check = %d, want 503", rec.Code)
	}

	srv2 := newTestServer(&fakeStore{}, &fakeQueue{}, &fakeWaker{})
	srv2.AddReadinessCheck("ok-dep", func(context.Context) error { return nil })
	rec2 := httptest.NewRecorder()
	srv2.Handler().ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("readyz with all checks passing = %d, want 200", rec2.Code)
	}
}

func TestRequestIDHeaderSet(t *testing.T) {
	srv := newTestServer(&fakeStore{}, &fakeQueue{}, &fakeWaker{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Header().Get(requestIDHeader) == "" {
		t.Fatal("expected X-Request-Id response header to be set")
	}
}
