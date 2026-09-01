package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"quisac/internal/config"
	"quisac/internal/core"
	"quisac/internal/db"
	"quisac/internal/traverse"
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
	return New(cfg, log, store, queue, nil, waker, nil)
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

// --- fault subgraph --------------------------------------------------------

type fakeIncidents struct {
	inc     db.Incident
	deleted []string
}

func (f *fakeIncidents) CreateIncident(context.Context, db.Incident) error { return nil }
func (f *fakeIncidents) ListIncidents(context.Context, string) ([]db.Incident, error) {
	return []db.Incident{f.inc}, nil
}
func (f *fakeIncidents) GetIncident(_ context.Context, id string) (db.Incident, error) {
	if f.inc.ID == id {
		return f.inc, nil
	}
	return db.Incident{}, db.ErrNotFound
}
func (f *fakeIncidents) DeleteIncident(_ context.Context, id, installationID string) error {
	if f.inc.ID != id || f.inc.InstallationID != installationID {
		return db.ErrNotFound
	}
	f.deleted = append(f.deleted, id)
	f.inc = db.Incident{}
	return nil
}

type fakeFaults struct {
	got traverse.FaultQuery
	err error
}

func (f *fakeFaults) FaultGraph(_ context.Context, q traverse.FaultQuery) (*traverse.FaultGraph, error) {
	f.got = q
	if f.err != nil {
		return nil, f.err
	}
	return &traverse.FaultGraph{
		Repo:        q.Repo,
		EntryPoints: q.EntryPointIDs,
		Nodes: []traverse.FaultNode{
			{ID: "svc.py::handle", Type: "Function", Name: "handle", Role: traverse.RoleEntry},
			{ID: "svc.py::parse", Type: "Function", Name: "parse", Role: traverse.RoleCandidate, Rank: 1, Score: 0.8},
		},
		Edges: []core.Edge{{From: "svc.py::parse", To: "svc.py::handle", Type: "Calls"}},
	}, nil
}

// traversedIncident is an incident that has a stored traversal to draw.
func traversedIncident() db.Incident {
	return db.Incident{
		ID:             "PD-1",
		InstallationID: "77",
		Repo:           "octo/hello",
		Status:         "traversed",
		EntryPoint:     []byte(`{"id":"svc.py::handle","type":"Function","name":"handle"}`),
		Candidates:     []byte(`[{"id":"svc.py::parse","type":"Function","name":"parse","file_path":"svc.py","score":0.8,"depth":1}]`),
	}
}

func newIncidentServer(incidents IncidentStore) *Server {
	cfg := &config.Config{}
	cfg.Server.SessionSecret = "test-secret"
	cfg.Resolution.MaxDepth = 5
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(cfg, log, authedStore(1), &fakeQueue{}, incidents, &fakeWaker{}, nil)
}

func TestFaultGraphReturnsSubgraph(t *testing.T) {
	faults := &fakeFaults{}
	srv := newIncidentServer(&fakeIncidents{inc: traversedIncident()}).WithFaultGraph(faults)

	req := httptest.NewRequest(http.MethodGet, "/api/incidents/PD-1/graph", nil)
	withSession(req)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	// The query must carry the stored result, not a re-derived one.
	if faults.got.Repo != "octo/hello" || len(faults.got.EntryPointIDs) != 1 ||
		faults.got.EntryPointIDs[0] != "svc.py::handle" {
		t.Fatalf("unexpected query: %+v", faults.got)
	}
	if len(faults.got.Candidates) != 1 || faults.got.Candidates[0].ID != "svc.py::parse" {
		t.Fatalf("candidates not passed through: %+v", faults.got.Candidates)
	}
	if faults.got.MaxDepth != 5 {
		t.Fatalf("MaxDepth = %d, want 5", faults.got.MaxDepth)
	}

	var got traverse.FaultGraph
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v (%s)", err, rec.Body)
	}
	if len(got.Nodes) != 2 || len(got.Edges) != 1 {
		t.Fatalf("subgraph = %d nodes / %d edges, want 2/1", len(got.Nodes), len(got.Edges))
	}
	if got.Nodes[0].Role != traverse.RoleEntry {
		t.Fatalf("first node role = %q, want entry", got.Nodes[0].Role)
	}
}

func TestFaultGraphScopesToInstallation(t *testing.T) {
	inc := traversedIncident()
	inc.InstallationID = "other-tenant"
	srv := newIncidentServer(&fakeIncidents{inc: inc}).WithFaultGraph(&fakeFaults{})

	req := httptest.NewRequest(http.MethodGet, "/api/incidents/PD-1/graph", nil)
	withSession(req)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestFaultGraphRequiresSession(t *testing.T) {
	srv := newIncidentServer(&fakeIncidents{inc: traversedIncident()}).WithFaultGraph(&fakeFaults{})

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/incidents/PD-1/graph", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// An incident resolved from the pattern cache never ran a traversal, so there is
// nothing to draw. That is an empty subgraph, not an error — and it must not
// reach Neo4j at all.
func TestFaultGraphWithoutTraversalIsEmpty(t *testing.T) {
	inc := traversedIncident()
	inc.EntryPoint = nil
	inc.Candidates = nil
	faults := &fakeFaults{}
	srv := newIncidentServer(&fakeIncidents{inc: inc}).WithFaultGraph(faults)

	req := httptest.NewRequest(http.MethodGet, "/api/incidents/PD-1/graph", nil)
	withSession(req)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if faults.got.Repo != "" {
		t.Fatalf("queried the graph for an untraversed incident: %+v", faults.got)
	}
	if !strings.Contains(rec.Body.String(), `"nodes":[]`) {
		t.Fatalf("want empty node list, got %s", rec.Body)
	}
}

func TestFaultGraphHidesInternalError(t *testing.T) {
	faults := &fakeFaults{err: errors.New("neo4j: connection refused at bolt://internal-host:7687")}
	srv := newIncidentServer(&fakeIncidents{inc: traversedIncident()}).WithFaultGraph(faults)

	req := httptest.NewRequest(http.MethodGet, "/api/incidents/PD-1/graph", nil)
	withSession(req)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "internal-host") {
		t.Fatalf("response leaked driver detail: %s", rec.Body)
	}
}

// Without a graph reader configured the rest of the incident API still works;
// only this endpoint reports itself unavailable.
func TestFaultGraphUnavailableWithoutReader(t *testing.T) {
	srv := newIncidentServer(&fakeIncidents{inc: traversedIncident()})

	req := httptest.NewRequest(http.MethodGet, "/api/incidents/PD-1/graph", nil)
	withSession(req)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestFaultGraphRejectsPost(t *testing.T) {
	srv := newIncidentServer(&fakeIncidents{inc: traversedIncident()}).WithFaultGraph(&fakeFaults{})

	req := httptest.NewRequest(http.MethodPost, "/api/incidents/PD-1/graph", nil)
	withSession(req)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

// --- resolve + delete ------------------------------------------------------

func TestDeleteIncidentRemovesRow(t *testing.T) {
	incidents := &fakeIncidents{inc: traversedIncident()}
	srv := newIncidentServer(incidents)

	req := httptest.NewRequest(http.MethodDelete, "/api/incidents/PD-1", nil)
	withSession(req)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body)
	}
	if len(incidents.deleted) != 1 || incidents.deleted[0] != "PD-1" {
		t.Fatalf("deleted = %v, want [PD-1]", incidents.deleted)
	}
}

// An incident belonging to another installation must not be deletable, and must
// not even be distinguishable from one that does not exist.
func TestDeleteIncidentRejectsOtherInstallation(t *testing.T) {
	inc := traversedIncident()
	inc.InstallationID = "99"
	incidents := &fakeIncidents{inc: inc}
	srv := newIncidentServer(incidents)

	req := httptest.NewRequest(http.MethodDelete, "/api/incidents/PD-1", nil)
	withSession(req)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if len(incidents.deleted) != 0 {
		t.Fatalf("deleted = %v, want none", incidents.deleted)
	}
}

func TestDeleteIncidentRequiresSession(t *testing.T) {
	incidents := &fakeIncidents{inc: traversedIncident()}
	srv := newIncidentServer(incidents)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/incidents/PD-1", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(incidents.deleted) != 0 {
		t.Fatalf("deleted = %v, want none", incidents.deleted)
	}
}

// --- patterns ---------------------------------------------------------------

type fakePatterns struct {
	list    []db.Pattern
	gotInst string
	err     error
}

func (f *fakePatterns) ListPatterns(_ context.Context, installationID string, _ int) ([]db.Pattern, error) {
	f.gotInst = installationID
	return f.list, f.err
}

func TestPatternsListsInstallationFixes(t *testing.T) {
	pats := &fakePatterns{list: []db.Pattern{{
		ID:             7,
		InstallationID: "77",
		Repo:           "octo/hello",
		Fingerprint:    "abc123",
		Signature:      "svc=shop|OperationalError|db is locked",
		RootCause:      "SQLite write lock held across the request",
		FixSummary:     "Commit before the outbound call",
		Edits:          []byte(`[{"path":"funcs.py","old_string":"a","new_string":"b"},{"path":"funcs.py","old_string":"c","new_string":"d"}]`),
		TimesSeen:      3,
		TimesApplied:   2,
	}}}
	srv := newIncidentServer(&fakeIncidents{inc: traversedIncident()}).WithPatterns(pats)

	req := httptest.NewRequest(http.MethodGet, "/api/patterns", nil)
	withSession(req)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	// Patterns are per-tenant; the handler must scope to the session's install.
	if pats.gotInst != "77" {
		t.Fatalf("installation = %q, want 77", pats.gotInst)
	}

	var got []patternRow
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body)
	}
	if len(got) != 1 {
		t.Fatalf("rows = %d, want 1", len(got))
	}
	// Edits are collapsed to the distinct files they touch — two edits on one
	// file is one file, and the raw before/after strings never leave the server.
	if len(got[0].Files) != 1 || got[0].Files[0] != "funcs.py" {
		t.Fatalf("files = %v, want [funcs.py]", got[0].Files)
	}
	if got[0].TimesApplied != 2 || got[0].RootCause == "" {
		t.Fatalf("row not mapped: %+v", got[0])
	}
	if strings.Contains(rec.Body.String(), "old_string") {
		t.Fatalf("raw edits leaked into the response: %s", rec.Body)
	}
}

func TestPatternsRequiresSession(t *testing.T) {
	srv := newIncidentServer(&fakeIncidents{}).WithPatterns(&fakePatterns{})

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/patterns", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// A deployment without the store wired must say so rather than serving an
// empty list, which would read as "no patterns learned".
func TestPatternsUnavailableWithoutStore(t *testing.T) {
	srv := newIncidentServer(&fakeIncidents{})

	req := httptest.NewRequest(http.MethodGet, "/api/patterns", nil)
	withSession(req)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// An installation with no patterns must serialise as [], not null — the view
// maps over the response directly.
func TestPatternsEmptyIsArray(t *testing.T) {
	srv := newIncidentServer(&fakeIncidents{}).WithPatterns(&fakePatterns{})

	req := httptest.NewRequest(http.MethodGet, "/api/patterns", nil)
	withSession(req)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Fatalf("body = %s, want []", body)
	}
}

// --- pagerduty webhook signature -------------------------------------------

const pdBody = `{"event":{"event_type":"incident.triggered","data":{"id":"PD-9","number":9,` +
	`"title":"boom","urgency":"high","service":{"summary":"checkout"}}}}`

func pdSignature(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

func pdServer(secret string) (*Server, *fakeQueue) {
	cfg := &config.Config{}
	cfg.Server.SessionSecret = "test-secret"
	cfg.Server.PagerDutyWebhookSecret = secret
	queue := &fakeQueue{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(cfg, log, authedStore(1), queue, &fakeIncidents{}, &fakeWaker{}, nil), queue
}

func pdPost(srv *Server, signature string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/pagerduty/webhook?installation_id=77", strings.NewReader(pdBody))
	if signature != "" {
		r.Header.Set("X-PagerDuty-Signature", signature)
	}
	w := httptest.NewRecorder()
	srv.handlePagerDuty(w, r)
	return w
}

func TestPagerDutyWebhookAcceptsValidSignature(t *testing.T) {
	srv, queue := pdServer("pd-secret")

	if got := pdPost(srv, pdSignature("pd-secret", pdBody)).Code; got != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", got, http.StatusAccepted)
	}
	if len(queue.enqueued) != 1 {
		t.Fatalf("enqueued %d jobs, want 1", len(queue.enqueued))
	}
}

func TestPagerDutyWebhookRejectsBadSignature(t *testing.T) {
	tests := []struct {
		name      string
		signature string
	}{
		{"wrong secret", pdSignature("not-the-secret", pdBody)},
		{"other payload", pdSignature("pd-secret", `{"event":{"data":{"id":"PD-X"}}}`)},
		{"missing header", ""},
		{"malformed", "garbage"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, queue := pdServer("pd-secret")

			if got := pdPost(srv, tt.signature).Code; got != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", got, http.StatusUnauthorized)
			}
			if len(queue.enqueued) != 0 {
				t.Fatalf("enqueued %d jobs for a rejected delivery, want 0", len(queue.enqueued))
			}
		})
	}
}

// Without a configured secret the endpoint stays open, which is the documented
// local-dev path.
func TestPagerDutyWebhookUnverifiedWithoutSecret(t *testing.T) {
	srv, queue := pdServer("")

	if got := pdPost(srv, "").Code; got != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", got, http.StatusAccepted)
	}
	if len(queue.enqueued) != 1 {
		t.Fatalf("enqueued %d jobs, want 1", len(queue.enqueued))
	}
}
