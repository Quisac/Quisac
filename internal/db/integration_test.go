package db

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"quisac/internal/core"
)

// These tests exercise the real SQL against a real Postgres. Every other test in
// this package works against fakes, which cannot catch a malformed UNNEST, a
// column list that drifted from its scan, or a pgx type mapping that does not
// hold — exactly the failures that only appear in production.
//
// They are skipped unless QUISAC_TEST_DATABASE_URL points at a scratch database.
// Run them with:
//
//	QUISAC_TEST_DATABASE_URL=postgres://user:pass@localhost:5432/scratch go test ./internal/db/
func testStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("QUISAC_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set QUISAC_TEST_DATABASE_URL to run database integration tests")
	}
	ctx := context.Background()
	store, err := Open(ctx, url)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(store.Close)
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store
}

func TestIntegrationFileStats(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	repo := "acme/stats-" + time.Now().Format("150405.000000")

	now := time.Now().UTC().Truncate(time.Second)

	// A history seed from commit scanning.
	if err := store.SeedFileChurn(ctx, repo, []core.FileChurn{
		{Path: "billing/refund.py", Changes: 12, LastChangedAt: now.Add(-48 * time.Hour)},
		{Path: "api/routes.py", Changes: 3, LastChangedAt: now.Add(-240 * time.Hour)},
	}); err != nil {
		t.Fatalf("SeedFileChurn: %v", err)
	}

	// Then a live push on top of it.
	if err := store.RecordFileChanges(ctx, repo, []string{"billing/refund.py"}, now); err != nil {
		t.Fatalf("RecordFileChanges: %v", err)
	}
	// And a shipped fix crediting the file with a fault.
	if err := store.RecordFaults(ctx, repo, []string{"billing/refund.py"}, now); err != nil {
		t.Fatalf("RecordFaults: %v", err)
	}

	stats, err := store.FileStats(ctx, repo)
	if err != nil {
		t.Fatalf("FileStats: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("got %d files, want 2", len(stats))
	}

	refund := stats["billing/refund.py"]
	if refund.ChangeCount != 13 {
		t.Errorf("change count = %d, want 13 (12 seeded + 1 push)", refund.ChangeCount)
	}
	if refund.FaultCount != 1 {
		t.Errorf("fault count = %d, want 1", refund.FaultCount)
	}
	if !refund.LastChangedAt.UTC().Equal(now) {
		t.Errorf("last changed = %v, want the push time %v", refund.LastChangedAt.UTC(), now)
	}
	if refund.LastFaultAt.IsZero() {
		t.Error("fault timestamp not recorded")
	}
	if routes := stats["api/routes.py"]; routes.FaultCount != 0 {
		t.Errorf("an untouched file accrued %d faults", routes.FaultCount)
	}

	// Re-seeding must overwrite rather than accumulate, or a re-index would
	// multiply every churn count.
	if err := store.SeedFileChurn(ctx, repo, []core.FileChurn{
		{Path: "billing/refund.py", Changes: 12, LastChangedAt: now.Add(-48 * time.Hour)},
	}); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	stats, _ = store.FileStats(ctx, repo)
	if got := stats["billing/refund.py"].ChangeCount; got != 12 {
		t.Errorf("re-seed produced change count %d, want it reset to 12", got)
	}
	// Fault history is earned from incidents and must survive a re-index.
	if got := stats["billing/refund.py"].FaultCount; got != 1 {
		t.Errorf("re-seed clobbered fault history: %d, want 1", got)
	}
}

func TestIntegrationPatterns(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	install := "inst-" + time.Now().Format("150405.000000")

	edits := json.RawMessage(`[{"path":"billing/refund.py","old_string":"a","new_string":"b"}]`)
	p := Pattern{
		InstallationID: install,
		Repo:           "acme/billing",
		Fingerprint:    "abc123",
		Signature:      "svc=billing-api | err=attributeerror",
		Tokens:         []string{"attributeerror", "nonetype", "balance"},
		ServiceName:    "billing-api",
		RootCause:      "nil customer on the refund path",
		FixSummary:     "guard against a missing customer",
		Edits:          edits,
	}

	id, err := store.SavePattern(ctx, p)
	if err != nil {
		t.Fatalf("SavePattern: %v", err)
	}

	got, err := store.FindPattern(ctx, install, "abc123")
	if err != nil {
		t.Fatalf("FindPattern: %v", err)
	}
	if got.ID != id || got.RootCause != p.RootCause {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if len(got.Tokens) != 3 || got.Tokens[0] != "attributeerror" {
		t.Errorf("tokens = %v, want the stored bag", got.Tokens)
	}
	if got.TimesSeen != 1 || got.TimesApplied != 0 {
		t.Errorf("counters = seen %d / applied %d, want 1 / 0", got.TimesSeen, got.TimesApplied)
	}

	// Seeing the same shape again bumps the counter rather than duplicating.
	if _, err := store.SavePattern(ctx, p); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	if err := store.MarkPatternApplied(ctx, id); err != nil {
		t.Fatalf("MarkPatternApplied: %v", err)
	}
	got, _ = store.FindPattern(ctx, install, "abc123")
	if got.TimesSeen != 2 || got.TimesApplied != 1 {
		t.Errorf("counters = seen %d / applied %d, want 2 / 1", got.TimesSeen, got.TimesApplied)
	}

	list, err := store.ListPatterns(ctx, install, 0)
	if err != nil {
		t.Fatalf("ListPatterns: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("got %d patterns, want 1 (the upsert must not duplicate)", len(list))
	}

	// Patterns are per-installation; another tenant must see nothing.
	if _, err := store.FindPattern(ctx, "someone-else", "abc123"); err == nil {
		t.Error("a pattern leaked across installations")
	}
}

func TestIntegrationIncidentResolutionRoundTrip(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	install := "inst-" + time.Now().Format("150405.000000")
	id := "PD-" + time.Now().Format("150405.000000")

	if err := store.CreateIncident(ctx, Incident{
		ID: id, InstallationID: install, PDNumber: 91,
		Title: "Refund failing", ServiceName: "billing-api",
		Urgency: "high", ErrorBody: "AttributeError: 'NoneType'",
	}); err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}

	inc, err := store.GetIncident(ctx, id)
	if err != nil {
		t.Fatalf("GetIncident: %v", err)
	}
	if inc.Resolution.Status != "pending" {
		t.Errorf("initial resolution status = %q, want pending", inc.Resolution.Status)
	}

	if err := store.SetFingerprint(ctx, id, "fp-abc"); err != nil {
		t.Fatalf("SetFingerprint: %v", err)
	}
	if err := store.MarkResolving(ctx, id); err != nil {
		t.Fatalf("MarkResolving: %v", err)
	}
	if err := store.SetIncidentRepo(ctx, id, "acme/billing"); err != nil {
		t.Fatalf("SetIncidentRepo: %v", err)
	}

	files := json.RawMessage(`[{"path":"billing/refund.py","additions":2,"deletions":0,"edits":1}]`)
	want := Resolution{
		Status: "resolved", Method: "llm", PatternID: 0,
		RootCause:  "order.customer is nil for guest checkouts",
		Confidence: 0.9, Summary: "guard against a missing customer",
		Files: files, PRURL: "https://github.com/acme/billing/pull/42",
		PRNumber: 42, Branch: "quisac/pd-1", InputTokens: 1200, OutputTokens: 300,
	}
	if err := store.SaveResolution(ctx, id, want); err != nil {
		t.Fatalf("SaveResolution: %v", err)
	}

	inc, err = store.GetIncident(ctx, id)
	if err != nil {
		t.Fatalf("GetIncident after save: %v", err)
	}
	r := inc.Resolution
	if r.Status != "resolved" || r.Method != "llm" || r.PRNumber != 42 {
		t.Errorf("resolution = %+v", r)
	}
	if r.InputTokens != 1200 || r.OutputTokens != 300 {
		t.Errorf("token spend = %d/%d, want 1200/300", r.InputTokens, r.OutputTokens)
	}
	if r.ResolvedAt == nil {
		t.Error("resolved_at was not stamped")
	}
	if inc.Fingerprint != "fp-abc" || inc.Repo != "acme/billing" {
		t.Errorf("fingerprint = %q, repo = %q", inc.Fingerprint, inc.Repo)
	}

	// The list query uses the same column list; a drift between them would only
	// show up here.
	list, err := store.ListIncidents(ctx, install)
	if err != nil {
		t.Fatalf("ListIncidents: %v", err)
	}
	if len(list) != 1 || list[0].Resolution.PRURL != want.PRURL {
		t.Errorf("list round-trip lost the resolution: %+v", list)
	}

	// A no_fix outcome must persist its reason and still stamp a terminal time.
	if err := store.SaveResolution(ctx, id, Resolution{
		Status: "no_fix", Method: "llm",
		RootCause: "cause is upstream", Error: "the cause is outside the provided files",
	}); err != nil {
		t.Fatalf("SaveResolution(no_fix): %v", err)
	}
	inc, _ = store.GetIncident(ctx, id)
	if inc.Resolution.Status != "no_fix" || inc.Resolution.Error == "" {
		t.Errorf("no_fix resolution = %+v", inc.Resolution)
	}
	if inc.Resolution.PRURL != "" {
		t.Errorf("stale PR url survived a later resolution: %q", inc.Resolution.PRURL)
	}
}

func TestIntegrationResolveJobRoundTrip(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	incidentID := "PD-job-" + time.Now().Format("150405.000000")

	// The 'resolve' kind must be accepted by the jobs CHECK constraint that
	// migration 0004 rewrites.
	job := Job{
		ID:             "job-" + time.Now().Format("150405.000000"),
		Kind:           JobResolve,
		Repo:           ResolveRepoKey(incidentID),
		InstallationID: "77",
		Payload:        []byte(`{"incident_id":"` + incidentID + `"}`),
		MaxAttempts:    2,
	}
	if err := store.EnqueueJob(ctx, job); err != nil {
		t.Fatalf("EnqueueJob(resolve): %v", err)
	}

	claimed, ok, err := store.ClaimJob(ctx)
	if err != nil {
		t.Fatalf("ClaimJob: %v", err)
	}
	if !ok {
		t.Fatal("no job claimed")
	}
	if claimed.Kind != JobResolve {
		t.Errorf("claimed kind = %q, want resolve", claimed.Kind)
	}
	if err := store.CompleteJob(ctx, claimed.ID, []byte(`{"status":"resolved"}`)); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}
}
