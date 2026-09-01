package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	gh "github.com/google/go-github/v75/github"

	"quisac/internal/core"
	"quisac/internal/db"
	"quisac/internal/github"
	"quisac/internal/llm"
	"quisac/internal/patch"
	"quisac/internal/traverse"
)

// ─── Fakes ───────────────────────────────────────────────────────────────────

type fakeStore struct {
	incident db.Incident
	pattern  db.Pattern
	hasPat   bool
	patterns []db.Pattern

	resolution   db.Resolution
	savedPattern *db.Pattern
	faultPaths   []string
	appliedID    int64
	repoSet      string
	fingerprint  string
}

func (s *fakeStore) GetIncident(context.Context, string) (db.Incident, error) { return s.incident, nil }
func (s *fakeStore) SetFingerprint(_ context.Context, _, fp string) error {
	s.fingerprint = fp
	return nil
}
func (s *fakeStore) MarkResolving(context.Context, string) error  { return nil }
func (s *fakeStore) MarkTraversing(context.Context, string) error { return nil }
func (s *fakeStore) UpdateIncidentResult(context.Context, string, string, any, any) error {
	return nil
}
func (s *fakeStore) FailIncidentTraversal(context.Context, string) error { return nil }
func (s *fakeStore) SaveResolution(_ context.Context, _ string, r db.Resolution) error {
	s.resolution = r
	return nil
}
func (s *fakeStore) SetIncidentRepo(_ context.Context, _, repo string) error {
	s.repoSet = repo
	return nil
}
func (s *fakeStore) ListIndexedRepos(context.Context, string) ([]string, error) {
	return []string{"acme/billing"}, nil
}
func (s *fakeStore) FindPattern(context.Context, string, string) (db.Pattern, error) {
	if s.hasPat {
		return s.pattern, nil
	}
	return db.Pattern{}, db.ErrNotFound
}
func (s *fakeStore) ListPatterns(context.Context, string, int) ([]db.Pattern, error) {
	return s.patterns, nil
}
func (s *fakeStore) SavePattern(_ context.Context, p db.Pattern) (int64, error) {
	s.savedPattern = &p
	return 7, nil
}
func (s *fakeStore) MarkPatternApplied(_ context.Context, id int64) error {
	s.appliedID = id
	return nil
}
func (s *fakeStore) RecordFaults(_ context.Context, _ string, paths []string, _ time.Time) error {
	s.faultPaths = paths
	return nil
}

type fakeTraverser struct {
	result *traverse.Result
	err    error
	calls  int
}

func (t *fakeTraverser) Run(context.Context, string, traverse.Incident, int) (*traverse.Result, error) {
	t.calls++
	if t.err != nil {
		return nil, t.err
	}
	return t.result, nil
}

type fakeSource struct {
	files   map[string]string
	prErr   error
	prOpts  *github.PROptions
	prCalls int
}

func (s *fakeSource) GetFileContent(_ context.Context, _, _, path, _ string) ([]byte, bool, error) {
	c, ok := s.files[path]
	if !ok {
		return nil, false, nil
	}
	return []byte(c), true, nil
}

func (s *fakeSource) OpenPullRequest(_ context.Context, _, _ string, opts github.PROptions) (*github.PullRequest, error) {
	s.prCalls++
	s.prOpts = &opts
	if s.prErr != nil {
		return nil, s.prErr
	}
	return &github.PullRequest{Number: 42, URL: "https://github.com/acme/billing/pull/42", Branch: opts.Branch}, nil
}

type fakeFixer struct {
	fix     *llm.Fix
	err     error
	calls   int
	enabled bool
	lastReq llm.FixRequest
}

func (f *fakeFixer) Enabled() bool { return f.enabled }
func (f *fakeFixer) Model() string { return "test-model" }
func (f *fakeFixer) ProposeFix(_ context.Context, req llm.FixRequest) (*llm.Fix, error) {
	f.calls++
	f.lastReq = req
	return f.fix, f.err
}

// ─── Fixtures ────────────────────────────────────────────────────────────────

const refundSource = `def process(order):
    amount = order.customer.balance
    return charge(amount)
`

const anchor = "def process(order):\n    amount = order.customer.balance"
const replacement = "def process(order):\n    if order.customer is None:\n        raise ValueError(\"no customer\")\n    amount = order.customer.balance"

func testIncident() db.Incident {
	return db.Incident{
		ID:             "PD-1",
		InstallationID: "77",
		Title:          "Refund failing",
		ServiceName:    "billing-api",
		ErrorBody: `Traceback (most recent call last):
  File "/srv/app/billing/refund.py", line 42, in process
    amount = order.customer.balance
AttributeError: 'NoneType' object has no attribute 'balance'`,
	}
}

func testResult() *traverse.Result {
	return &traverse.Result{
		Repo:       "acme/billing",
		EntryPoint: &core.Node{ID: "func:billing/refund.py::process", Type: "Function", Name: "process"},
		FramePaths: []string{"billing/refund.py"},
		Candidates: []traverse.CandidateNode{{
			ID: "func:billing/refund.py::process", Type: "Function", Name: "process",
			FilePath: "billing/refund.py", Depth: 0, Score: 0.82,
			Reasons: []string{"the failing symbol itself"},
		}},
	}
}

func newHarness(t *testing.T, cfg Config) (*Resolver, *fakeStore, *fakeTraverser, *fakeSource, *fakeFixer) {
	t.Helper()
	store := &fakeStore{incident: testIncident()}
	trav := &fakeTraverser{result: testResult()}
	src := &fakeSource{files: map[string]string{"billing/refund.py": refundSource}}
	fixer := &fakeFixer{enabled: true, fix: &llm.Fix{
		RootCause:    "order.customer is nil for guest checkouts",
		Summary:      "guard against a missing customer on the refund path",
		Confidence:   0.9,
		InputTokens:  1200,
		OutputTokens: 300,
		Edits:        []patch.Edit{{Path: "billing/refund.py", Old: anchor, New: replacement, Why: "nil guard"}},
	}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(store, trav, src, fixer, log, cfg), store, trav, src, fixer
}

func storedPattern(t *testing.T, edits []patch.Edit) db.Pattern {
	t.Helper()
	raw, err := json.Marshal(edits)
	if err != nil {
		t.Fatal(err)
	}
	// The fingerprint is recomputed by the resolver; FindPattern is faked, so any
	// value works here.
	return db.Pattern{
		ID: 5, InstallationID: "77", Repo: "acme/billing",
		RootCause: "known nil-customer defect", FixSummary: "guard against a missing customer",
		Edits: raw, TimesSeen: 3, TimesApplied: 2,
	}
}

// ─── Tier 1: pattern cache ───────────────────────────────────────────────────

func TestPatternHitSkipsGraphAndModel(t *testing.T) {
	r, store, trav, src, fixer := newHarness(t, Config{AutoPR: true})
	store.hasPat = true
	store.pattern = storedPattern(t, []patch.Edit{{Path: "billing/refund.py", Old: anchor, New: replacement}})

	out, err := r.Resolve(context.Background(), "PD-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if out.Status != "resolved" || out.Method != "pattern" {
		t.Fatalf("outcome = %+v, want resolved via pattern", out)
	}
	// The whole point of tier 1: neither the graph nor the model is touched.
	if trav.calls != 0 {
		t.Errorf("traverser called %d times on a pattern hit", trav.calls)
	}
	if fixer.calls != 0 {
		t.Errorf("model called %d times on a pattern hit", fixer.calls)
	}
	if out.Tokens != 0 {
		t.Errorf("token spend = %d, want 0", out.Tokens)
	}
	if src.prCalls != 1 {
		t.Errorf("pull requests opened = %d, want 1", src.prCalls)
	}
	if store.appliedID != 5 {
		t.Errorf("pattern %d marked applied, want 5", store.appliedID)
	}
	if store.repoSet != "acme/billing" {
		t.Errorf("incident repo = %q, want acme/billing", store.repoSet)
	}
}

func TestStalePatternFallsThroughToModel(t *testing.T) {
	// The cached fix anchors on code that has since changed. That must not
	// dead-end the incident — it falls through to the graph and the model.
	r, store, trav, src, fixer := newHarness(t, Config{AutoPR: true})
	store.hasPat = true
	store.pattern = storedPattern(t, []patch.Edit{{
		Path: "billing/refund.py",
		Old:  "def process(order, retries=3):", // no longer in the file
		New:  "def process(order, retries=5):",
	}})

	out, err := r.Resolve(context.Background(), "PD-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if out.Method != "llm" {
		t.Fatalf("method = %q, want the run to fall through to the model", out.Method)
	}
	if trav.calls == 0 || fixer.calls == 0 {
		t.Errorf("fall-through skipped a tier (traverse=%d, model=%d)", trav.calls, fixer.calls)
	}
	if out.Status != "resolved" || src.prCalls != 1 {
		t.Errorf("outcome = %+v with %d PRs, want a resolved fix", out, src.prCalls)
	}
}

func TestFuzzyPatternMatchBelowThresholdIsIgnored(t *testing.T) {
	r, store, _, _, fixer := newHarness(t, Config{AutoPR: true})
	// No exact match, and the only stored pattern shares nothing with this alert.
	store.patterns = []db.Pattern{{
		ID: 9, Repo: "acme/billing", Tokens: []string{"gateway", "timeout", "connection", "upstream"},
		Edits: []byte(`[]`),
	}}

	if _, err := r.Resolve(context.Background(), "PD-1"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if fixer.calls != 1 {
		t.Errorf("model called %d times; an unrelated pattern should not have matched", fixer.calls)
	}
}

// ─── Tier 3: model fix ───────────────────────────────────────────────────────

// A provider outage says nothing about the incident. It must leave the incident
// queued and hand the job back for retry, not burn it as permanently failed.
func TestTransientModelFailureLeavesIncidentQueued(t *testing.T) {
	r, store, _, src, _ := newHarness(t, Config{AutoPR: true})
	fixer := &fakeFixer{enabled: true, err: fmt.Errorf("%w: 503 model overloaded", llm.ErrTransient)}
	r.fixer = fixer

	_, err := r.Resolve(context.Background(), "PD-1")
	if err == nil {
		t.Fatal("Resolve returned nil; a transient provider failure must be retryable")
	}
	if !errors.Is(err, llm.ErrTransient) {
		t.Fatalf("error = %v, want it to wrap llm.ErrTransient", err)
	}
	if store.resolution.Status != "pending" {
		t.Errorf("resolution status = %q, want pending", store.resolution.Status)
	}
	if store.resolution.Error != "" {
		t.Errorf("resolution error = %q, want empty; an outage is not a diagnosis", store.resolution.Error)
	}
	if src.prCalls != 0 {
		t.Errorf("pull requests opened = %d, want 0", src.prCalls)
	}
}

// A failure the provider actually answered with is the incident's outcome, and
// retrying it would only spend the same tokens on the same answer.
func TestPermanentModelFailureIsTerminal(t *testing.T) {
	r, store, _, _, _ := newHarness(t, Config{AutoPR: true})
	r.fixer = &fakeFixer{enabled: true, err: fmt.Errorf("llm: propose fix: 400 bad request")}

	out, err := r.Resolve(context.Background(), "PD-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out.Status != "failed" {
		t.Fatalf("outcome status = %q, want failed", out.Status)
	}
	if !strings.Contains(store.resolution.Error, "model call") {
		t.Errorf("resolution error = %q, want it to name the model call", store.resolution.Error)
	}
}

func TestModelFixOpensPullRequestAndRecordsPattern(t *testing.T) {
	r, store, _, src, _ := newHarness(t, Config{AutoPR: true, DraftPR: true})

	out, err := r.Resolve(context.Background(), "PD-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if out.Status != "resolved" || out.Method != "llm" {
		t.Fatalf("outcome = %+v, want resolved via llm", out)
	}
	if out.Tokens != 1500 {
		t.Errorf("token spend = %d, want 1500", out.Tokens)
	}
	if src.prOpts == nil {
		t.Fatal("no pull request opened")
	}
	if !src.prOpts.Draft {
		t.Error("DraftPR was set but the pull request was not a draft")
	}
	if !strings.Contains(src.prOpts.Title, "guard against a missing customer") {
		t.Errorf("PR title = %q, want it to carry the fix summary", src.prOpts.Title)
	}
	if len(src.prOpts.Files) != 1 || !strings.Contains(src.prOpts.Files[0].Content, "raise ValueError") {
		t.Error("the committed file does not contain the fix")
	}
	if !strings.Contains(src.prOpts.Body, "Root cause") {
		t.Error("PR body is missing the root-cause section")
	}

	// A shipped fix teaches the cache, so the next occurrence costs nothing.
	if store.savedPattern == nil {
		t.Fatal("no pattern recorded for a shipped fix")
	}
	if store.savedPattern.Fingerprint != store.fingerprint {
		t.Error("recorded pattern does not use the incident's fingerprint")
	}
	if store.faultPaths == nil || store.faultPaths[0] != "billing/refund.py" {
		t.Errorf("fault history = %v, want the changed file credited", store.faultPaths)
	}
}

func TestOnlyGraphSelectedFilesReachTheModel(t *testing.T) {
	r, _, _, src, fixer := newHarness(t, Config{AutoPR: true, MaxFixFiles: 3})
	// Files the graph did not select must never be sent, however many exist.
	src.files["billing/unrelated.py"] = "x = 1\n"
	src.files["api/routes.py"] = "y = 2\n"

	if _, err := r.Resolve(context.Background(), "PD-1"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(fixer.lastReq.Files) != 1 {
		t.Fatalf("model saw %d files, want only the 1 the graph selected", len(fixer.lastReq.Files))
	}
	if _, ok := fixer.lastReq.Files["billing/refund.py"]; !ok {
		t.Error("the model did not receive the file the stack trace pointed at")
	}
}

func TestModelDeclineIsRecordedNotFailed(t *testing.T) {
	r, store, _, src, fixer := newHarness(t, Config{AutoPR: true})
	fixer.fix = &llm.Fix{
		RootCause:    "the failure originates in the payment gateway, not this codebase",
		NoFixReason:  "the cause is outside the provided files",
		InputTokens:  900,
		OutputTokens: 120,
	}

	out, err := r.Resolve(context.Background(), "PD-1")
	if err != nil {
		t.Fatalf("a decline must not be an error: %v", err)
	}
	if out.Status != "no_fix" {
		t.Errorf("status = %q, want no_fix", out.Status)
	}
	if src.prCalls != 0 {
		t.Error("a pull request was opened despite the model declining")
	}
	// The diagnosis is the deliverable even when the patch is not.
	if !strings.Contains(store.resolution.RootCause, "payment gateway") {
		t.Error("the root-cause analysis was discarded")
	}
	if store.savedPattern != nil {
		t.Error("a pattern was recorded for a fix that never shipped")
	}
}

func TestLowConfidenceFixIsWithheld(t *testing.T) {
	r, store, _, src, fixer := newHarness(t, Config{AutoPR: true, MinConfidence: 0.8})
	fixer.fix.Confidence = 0.4

	out, err := r.Resolve(context.Background(), "PD-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out.Status != "no_fix" {
		t.Errorf("status = %q, want the fix withheld", out.Status)
	}
	if src.prCalls != 0 {
		t.Error("a low-confidence fix was opened as a pull request")
	}
	if !strings.Contains(store.resolution.Error, "confidence") {
		t.Errorf("reason = %q, want it to name the confidence threshold", store.resolution.Error)
	}
}

func TestInvalidPatchNeverReachesGitHub(t *testing.T) {
	r, store, _, src, fixer := newHarness(t, Config{AutoPR: true})
	fixer.fix.Edits = []patch.Edit{{
		Path: "billing/refund.py",
		Old:  "def settle(order):", // the model invented code that is not there
		New:  "def settle(order, retry=True):",
	}}

	out, err := r.Resolve(context.Background(), "PD-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out.Status != "failed" {
		t.Errorf("status = %q, want failed", out.Status)
	}
	if src.prCalls != 0 {
		t.Fatal("a patch that does not apply reached GitHub")
	}
	if !strings.Contains(store.resolution.Error, "did not apply") {
		t.Errorf("error = %q, want it to explain the rejection", store.resolution.Error)
	}
}

func TestAutoPRDisabledValidatesButDoesNotPush(t *testing.T) {
	r, store, _, src, _ := newHarness(t, Config{AutoPR: false})

	out, err := r.Resolve(context.Background(), "PD-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if src.prCalls != 0 {
		t.Error("a pull request was opened with auto-PR disabled")
	}
	if out.Status != "no_fix" {
		t.Errorf("status = %q, want no_fix", out.Status)
	}
	if !strings.Contains(store.resolution.Error, "QUISAC_AUTO_PR") {
		t.Errorf("reason = %q, want it to name the setting", store.resolution.Error)
	}
	// The fix itself was still validated, so the reviewer can see what it would be.
	if store.resolution.Files == nil {
		t.Error("the validated change was not recorded")
	}
}

// A patch that validated against the real files is a good answer even when it
// could not be published. Reporting that as "failed" tells the on-call engineer
// the analysis broke, when in fact a correct fix is sitting there waiting.
func TestUnpublishablePullRequestKeepsTheValidatedFix(t *testing.T) {
	r, store, _, src, _ := newHarness(t, Config{AutoPR: true})
	src.prErr = fmt.Errorf("resource not accessible by integration")

	out, err := r.Resolve(context.Background(), "PD-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out.Status != "no_fix" {
		t.Errorf("status = %q, want no_fix; the analysis succeeded, only publishing did not", out.Status)
	}
	if !strings.Contains(store.resolution.Error, "not accessible") {
		t.Errorf("error = %q, want the GitHub failure surfaced", store.resolution.Error)
	}
	if store.resolution.RootCause == "" {
		t.Error("the root cause was discarded along with the pull request")
	}
	if store.resolution.Files == nil {
		t.Error("the validated change was not recorded")
	}
	if store.savedPattern != nil {
		t.Error("a pattern was recorded for a fix that never opened")
	}
}

// A 403 from GitHub is an App permissions problem, and saying so is the whole
// difference between a two-minute fix and an afternoon spent doubting the model.
func TestPermissionDeniedNamesTheMissingScopes(t *testing.T) {
	r, store, _, src, _ := newHarness(t, Config{AutoPR: true})
	src.prErr = &gh.ErrorResponse{
		Response: &http.Response{StatusCode: http.StatusForbidden},
		Message:  "Resource not accessible by integration",
	}

	if _, err := r.Resolve(context.Background(), "PD-1"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, want := range []string{"contents: write", "pull_requests: write", "acme/billing"} {
		if !strings.Contains(store.resolution.Error, want) {
			t.Errorf("error = %q, want it to mention %q", store.resolution.Error, want)
		}
	}
}

// ─── Degraded deployments ────────────────────────────────────────────────────

func TestWithoutModelTheGraphAnalysisIsStillDelivered(t *testing.T) {
	r, store, trav, src, fixer := newHarness(t, Config{AutoPR: true})
	fixer.enabled = false

	out, err := r.Resolve(context.Background(), "PD-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out.Status != "no_fix" {
		t.Errorf("status = %q, want no_fix", out.Status)
	}
	if trav.calls == 0 {
		t.Error("traversal was skipped; the graph analysis is the floor")
	}
	if src.prCalls != 0 {
		t.Error("a pull request was opened without a model")
	}
	if !strings.Contains(store.resolution.RootCause, "process") {
		t.Errorf("root cause = %q, want the graph's top candidate", store.resolution.RootCause)
	}
}

func TestTraversalFailureIsRecorded(t *testing.T) {
	r, store, trav, _, fixer := newHarness(t, Config{AutoPR: true})
	trav.err = fmt.Errorf("no matching node")

	out, err := r.Resolve(context.Background(), "PD-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out.Status != "failed" {
		t.Errorf("status = %q, want failed", out.Status)
	}
	if fixer.calls != 0 {
		t.Error("the model was called without any files to give it")
	}
	if store.resolution.Error == "" {
		t.Error("the traversal failure was not recorded")
	}
}

func TestFingerprintIsStoredBeforeAnyFixAttempt(t *testing.T) {
	r, store, trav, _, _ := newHarness(t, Config{AutoPR: true})
	trav.err = fmt.Errorf("boom")

	if _, err := r.Resolve(context.Background(), "PD-1"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Even a failed run must leave the key a later incident can match on.
	if store.fingerprint == "" {
		t.Error("no fingerprint stored for a failed resolution")
	}
}
