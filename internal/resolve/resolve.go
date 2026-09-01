// Package resolve orchestrates the incident-to-pull-request pipeline.
//
// It runs three tiers, cheapest first, stopping at the first that yields a fix:
//
//	tier 1  pattern cache   — an error shape fixed before; no walk, no model call
//	tier 2  graph traversal — walk back from the failure to ranked candidates
//	tier 3  model fix       — ask for a patch over the files the graph selected
//
// Every tier may fall through to the next, and whatever one produces is
// validated the same way: the patch must apply to the current file contents by
// exact match, or it is refused. A surviving fix is committed to a branch and
// opened as a pull request.
package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"quisac/internal/db"
	"quisac/internal/github"
	"quisac/internal/llm"
	"quisac/internal/patch"
	"quisac/internal/patterns"
	"quisac/internal/traverse"
)

// Store is the persistence the pipeline needs. Satisfied by *db.Store.
type Store interface {
	GetIncident(ctx context.Context, id string) (db.Incident, error)
	SetFingerprint(ctx context.Context, id, fingerprint string) error
	MarkResolving(ctx context.Context, id string) error
	MarkTraversing(ctx context.Context, id string) error
	UpdateIncidentResult(ctx context.Context, id, repo string, entryPoint, candidates any) error
	FailIncidentTraversal(ctx context.Context, id string) error
	SaveResolution(ctx context.Context, id string, r db.Resolution) error
	SetIncidentRepo(ctx context.Context, id, repo string) error
	ListIndexedRepos(ctx context.Context, installationID string) ([]string, error)

	FindPattern(ctx context.Context, installationID, fingerprint string) (db.Pattern, error)
	ListPatterns(ctx context.Context, installationID string, limit int) ([]db.Pattern, error)
	SavePattern(ctx context.Context, p db.Pattern) (int64, error)
	MarkPatternApplied(ctx context.Context, id int64) error

	RecordFaults(ctx context.Context, repo string, paths []string, at time.Time) error
}

// Traverser walks the code graph. Satisfied by *traverse.Traverser.
type Traverser interface {
	Run(ctx context.Context, repo string, incident traverse.Incident, maxDepth int) (*traverse.Result, error)
}

// Source reads repository files and opens pull requests. Satisfied by *github.App.
type Source interface {
	GetFileContent(ctx context.Context, installationID, repo, path, ref string) ([]byte, bool, error)
	OpenPullRequest(ctx context.Context, installationID, repo string, opts github.PROptions) (*github.PullRequest, error)
}

// Fixer proposes patches. Satisfied by every client in package llm, and by nil:
// an unconfigured model is a supported deployment, not an error.
type Fixer interface {
	Enabled() bool
	Model() string
	ProposeFix(ctx context.Context, req llm.FixRequest) (*llm.Fix, error)
}

// Config tunes the pipeline. Zero values fall back to the defaults below.
type Config struct {
	MaxDepth      int     // BFS hops back from the entry point
	MaxFixFiles   int     // files sent to the model
	MaxFileBytes  int     // per-file size ceiling for the fix context
	MinConfidence float64 // below this, a proposed fix is not opened as a PR
	Similarity    float64 // Jaccard threshold for a fuzzy pattern match
	BranchPrefix  string  // branch namespace for generated fixes
	DraftPR       bool    // open pull requests as drafts
	AutoPR        bool    // open pull requests without human approval
}

func (c Config) withDefaults() Config {
	if c.MaxDepth <= 0 {
		c.MaxDepth = 5
	}
	if c.MaxFixFiles <= 0 {
		c.MaxFixFiles = 3
	}
	if c.MaxFileBytes <= 0 {
		c.MaxFileBytes = 256 << 10
	}
	if c.MinConfidence <= 0 {
		c.MinConfidence = 0.5
	}
	if c.Similarity <= 0 {
		c.Similarity = patterns.DefaultSimilarity
	}
	if c.BranchPrefix == "" {
		c.BranchPrefix = "quisac"
	}
	return c
}

// Resolver runs the pipeline for one incident at a time. It is safe for
// concurrent use; serialisation per incident is the job queue's responsibility.
type Resolver struct {
	store     Store
	traverser Traverser
	src       Source
	fixer     Fixer
	log       *slog.Logger
	cfg       Config
}

// New builds a Resolver. A nil traverser or fixer skips that tier.
func New(store Store, traverser Traverser, src Source, fixer Fixer, log *slog.Logger, cfg Config) *Resolver {
	if log == nil {
		log = slog.Default()
	}
	return &Resolver{
		store:     store,
		traverser: traverser,
		src:       src,
		fixer:     fixer,
		log:       log,
		cfg:       cfg.withDefaults(),
	}
}

// Outcome summarises a completed run, for logging and for the job record.
type Outcome struct {
	IncidentID string `json:"incident_id"`
	Status     string `json:"status"` // resolved | no_fix | failed | gone
	Method     string `json:"method,omitempty"`
	Repo       string `json:"repo,omitempty"`
	PRURL      string `json:"pr_url,omitempty"`
	Tokens     int    `json:"tokens"`
	Duration   string `json:"duration"`
}

// Resolve runs the full pipeline for one incident. It errors only for a failure
// worth retrying: a diagnosed incident with no fix is a successful 'no_fix' run.
func (r *Resolver) Resolve(ctx context.Context, incidentID string) (Outcome, error) {
	start := time.Now()

	inc, err := r.store.GetIncident(ctx, incidentID)
	if err != nil {
		// Deleted while the job sat in the queue: nothing to fix, nothing to retry.
		if errors.Is(err, db.ErrNotFound) {
			r.log.Info("resolve: incident gone, skipping", "incident", incidentID)
			return Outcome{
				IncidentID: incidentID,
				Status:     "gone",
				Duration:   time.Since(start).String(),
			}, nil
		}
		return Outcome{}, fmt.Errorf("resolve: load incident %s: %w", incidentID, err)
	}

	log := r.log.With("incident", inc.ID, "service", inc.ServiceName)
	tinc := traverse.Incident{
		ID:             inc.ID,
		InstallationID: inc.InstallationID,
		Title:          inc.Title,
		ServiceName:    inc.ServiceName,
		ErrorBody:      inc.ErrorBody,
	}

	// Written first, so even a failed run leaves the key that lets a later
	// incident recognise this one.
	frames := traverse.ExtractFrames(inc.ErrorBody)
	sig := patterns.Analyze(inc.Title, inc.ErrorBody, inc.ServiceName, frames)
	if err := r.store.SetFingerprint(ctx, inc.ID, sig.Fingerprint); err != nil {
		log.Warn("store fingerprint", "err", err)
	}
	if err := r.store.MarkResolving(ctx, inc.ID); err != nil {
		log.Warn("mark resolving", "err", err)
	}

	finish := func(res db.Resolution) (Outcome, error) {
		if err := r.store.SaveResolution(ctx, inc.ID, res); err != nil {
			return Outcome{}, fmt.Errorf("resolve: save resolution: %w", err)
		}
		out := Outcome{
			IncidentID: inc.ID,
			Status:     res.Status,
			Method:     res.Method,
			PRURL:      res.PRURL,
			Tokens:     res.InputTokens + res.OutputTokens,
			Duration:   time.Since(start).Round(time.Millisecond).String(),
		}
		log.Info("resolution finished", "status", res.Status, "method", res.Method,
			"tokens", out.Tokens, "pr", res.PRURL, "dur", out.Duration)
		return out, nil
	}

	// Tier 1: a fix we already know.
	if res, ok := r.tryPattern(ctx, log, inc, sig); ok {
		return finish(res)
	}

	// Tier 2: the graph.
	result, err := r.traverseIncident(ctx, log, inc, tinc)
	if err != nil {
		return finish(db.Resolution{
			Status: "failed",
			Error:  err.Error(),
		})
	}

	// Tier 3: the model.
	res, err := r.tryModel(ctx, log, inc, tinc, sig, result)
	if err != nil {
		// A capacity blip is not an answer: save as still-pending so the feed
		// keeps showing it queued, and return the error so the queue retries.
		if saveErr := r.store.SaveResolution(ctx, inc.ID, res); saveErr != nil {
			log.Warn("save pending resolution", "err", saveErr)
		}
		return Outcome{}, err
	}
	return finish(res)
}

// tryPattern attempts to resolve the incident from a previously recorded fix.
// Every fall-through case returns ok=false, so the caller continues to the graph
// rather than reporting a failure.
func (r *Resolver) tryPattern(ctx context.Context, log *slog.Logger, inc db.Incident, sig patterns.Signature) (db.Resolution, bool) {
	p, similarity, found := r.matchPattern(ctx, log, inc.InstallationID, sig)
	if !found {
		return db.Resolution{}, false
	}

	var edits []patch.Edit
	if err := json.Unmarshal(p.Edits, &edits); err != nil || len(edits) == 0 {
		log.Warn("stored pattern has unusable edits", "pattern", p.ID, "err", err)
		return db.Resolution{}, false
	}

	log.Info("pattern matched", "pattern", p.ID, "repo", p.Repo,
		"similarity", similarity, "times_applied", p.TimesApplied)

	files, _, err := r.fetchFiles(ctx, inc.InstallationID, p.Repo, patch.Paths(edits))
	if err != nil {
		log.Warn("pattern fetch files failed; falling through to graph", "err", err)
		return db.Resolution{}, false
	}

	// If the file has moved on, the pattern is stale: fall through to the graph.
	changes, err := patch.Apply(files, edits)
	if err != nil {
		log.Info("pattern no longer applies; falling through to graph", "pattern", p.ID, "err", err)
		return db.Resolution{}, false
	}

	if err := r.store.SetIncidentRepo(ctx, inc.ID, p.Repo); err != nil {
		log.Warn("set incident repo", "err", err)
	}

	res := db.Resolution{
		Status:     "resolved",
		Method:     "pattern",
		PatternID:  p.ID,
		RootCause:  p.RootCause,
		Summary:    p.FixSummary,
		Confidence: similarity,
	}
	r.ship(ctx, log, inc, p.Repo, changes, &res, patternPRBody(p, similarity, sig))

	if res.Status == "resolved" {
		if err := r.store.MarkPatternApplied(ctx, p.ID); err != nil {
			log.Warn("mark pattern applied", "err", err)
		}
	}
	return res, true
}

// matchPattern finds a stored fix for this error shape: an exact fingerprint hit
// first, then the closest fuzzy match above the configured threshold.
func (r *Resolver) matchPattern(ctx context.Context, log *slog.Logger, installationID string, sig patterns.Signature) (db.Pattern, float64, bool) {
	if p, err := r.store.FindPattern(ctx, installationID, sig.Fingerprint); err == nil {
		return p, 1.0, true
	}

	// The same bug surfaces with different wording, so fall back to comparing
	// token bags — but only a high overlap counts. Applying the wrong cached fix
	// is far worse than falling through to the model.
	stored, err := r.store.ListPatterns(ctx, installationID, 0)
	if err != nil {
		log.Warn("list patterns", "err", err)
		return db.Pattern{}, 0, false
	}

	var (
		best    db.Pattern
		bestSim float64
	)
	for _, p := range stored {
		if s := patterns.Similarity(sig.Tokens, p.Tokens); s > bestSim {
			best, bestSim = p, s
		}
	}
	if bestSim >= r.cfg.Similarity {
		return best, bestSim, true
	}
	return db.Pattern{}, 0, false
}

// traverseIncident runs the graph walk across every repo the installation has
// indexed and keeps the strongest result.
func (r *Resolver) traverseIncident(ctx context.Context, log *slog.Logger, inc db.Incident, tinc traverse.Incident) (*traverse.Result, error) {
	if r.traverser == nil {
		return nil, fmt.Errorf("traversal engine unavailable (Neo4j not connected)")
	}

	repos, err := r.store.ListIndexedRepos(ctx, inc.InstallationID)
	if err != nil {
		return nil, fmt.Errorf("list indexed repos: %w", err)
	}
	if len(repos) == 0 {
		return nil, fmt.Errorf("no indexed repositories for this installation; index a repo first")
	}

	if err := r.store.MarkTraversing(ctx, inc.ID); err != nil {
		log.Warn("mark traversing", "err", err)
	}

	// An alert names a service, not a repository, so every indexed repo is a
	// candidate. The one holding the failing code matches its stack frames and
	// outscores repos that merely share a word with the service name.
	var (
		best     *traverse.Result
		bestPeak float64
	)
	for _, repo := range repos {
		res, err := r.traverser.Run(ctx, repo, tinc, r.cfg.MaxDepth)
		if err != nil {
			log.Debug("traversal found nothing in repo", "repo", repo, "err", err)
			continue
		}
		peak := 0.0
		if len(res.Candidates) > 0 {
			peak = res.Candidates[0].Score
		}
		if best == nil || peak > bestPeak {
			best, bestPeak = res, peak
		}
	}

	if best == nil {
		if err := r.store.FailIncidentTraversal(ctx, inc.ID); err != nil {
			log.Warn("fail traversal", "err", err)
		}
		return nil, fmt.Errorf("could not locate the failing code in any indexed repository")
	}

	if err := r.store.UpdateIncidentResult(ctx, inc.ID, best.Repo, best.EntryPoint, best.Candidates); err != nil {
		log.Warn("store traversal result", "err", err)
	}
	log.Info("traversal complete", "repo", best.Repo, "candidates", len(best.Candidates),
		"top_score", bestPeak, "frames", len(best.FramePaths))
	return best, nil
}

// tryModel asks the model for a patch over the files the graph selected. It
// errors only on a transient provider failure; every judgement the model
// actually made — a decline included — is a resolution with a nil error.
func (r *Resolver) tryModel(
	ctx context.Context,
	log *slog.Logger,
	inc db.Incident,
	tinc traverse.Incident,
	sig patterns.Signature,
	result *traverse.Result,
) (db.Resolution, error) {
	if r.fixer == nil || !r.fixer.Enabled() {
		return db.Resolution{
			Status:    "no_fix",
			RootCause: graphOnlyDiagnosis(result),
			Error:     "automated fixes are not configured (no model API key); the ranked candidates above are the analysis",
		}, nil
	}

	paths := result.TopPaths(r.cfg.MaxFixFiles)
	if len(paths) == 0 {
		return db.Resolution{
			Status:    "no_fix",
			RootCause: graphOnlyDiagnosis(result),
			Error:     "traversal produced no file-backed candidates to inspect",
		}, nil
	}

	files, skipped, err := r.fetchFiles(ctx, inc.InstallationID, result.Repo, paths)
	if err != nil {
		return db.Resolution{Status: "failed", Error: fmt.Sprintf("fetch fix context: %v", err)}, nil
	}
	if len(files) == 0 {
		return db.Resolution{
			Status:    "no_fix",
			RootCause: graphOnlyDiagnosis(result),
			Error:     "none of the candidate files could be read from GitHub",
		}, nil
	}

	log.Info("requesting fix", "repo", result.Repo, "files", len(files),
		"skipped", len(skipped), "model", r.fixer.Model())

	fix, err := r.fixer.ProposeFix(ctx, llm.FixRequest{
		Repo:     result.Repo,
		Incident: tinc,
		Result:   result,
		Files:    files,
		Skipped:  skipped,
	})
	if err != nil {
		// Overloaded or rate-limited says nothing about this incident, so it must
		// not be recorded as the outcome. Stay queued and let the job retry.
		if errors.Is(err, llm.ErrTransient) {
			log.Warn("model unavailable; leaving the incident queued for retry", "err", err)
			return db.Resolution{Status: "pending"}, err
		}
		return db.Resolution{Status: "failed", Error: fmt.Sprintf("model call: %v", err)}, nil
	}

	res := db.Resolution{
		Method:       "llm",
		RootCause:    fix.RootCause,
		Summary:      fix.Summary,
		Confidence:   fix.Confidence,
		InputTokens:  fix.InputTokens,
		OutputTokens: fix.OutputTokens,
	}

	if !fix.Proposed() {
		res.Status = "no_fix"
		res.Error = fix.NoFixReason
		log.Info("model declined to propose a fix", "reason", fix.NoFixReason)
		return res, nil
	}
	if fix.Confidence < r.cfg.MinConfidence {
		res.Status = "no_fix"
		res.Error = fmt.Sprintf("fix withheld: confidence %.2f is below the %.2f threshold for opening a pull request",
			fix.Confidence, r.cfg.MinConfidence)
		log.Info("fix withheld on confidence", "confidence", fix.Confidence)
		return res, nil
	}

	// A failed apply means the model referenced code that is not there — worth
	// surfacing rather than silently retrying.
	changes, err := patch.Apply(files, fix.Edits)
	if err != nil {
		res.Status = "failed"
		res.Error = fmt.Sprintf("proposed fix did not apply to the current source: %v", err)
		log.Warn("patch rejected", "err", err)
		return res, nil
	}

	r.ship(ctx, log, inc, result.Repo, changes, &res, modelPRBody(inc, result, fix))

	// Recording a patch that never opened would teach the cache to replay it.
	if res.Status == "resolved" {
		r.recordPattern(ctx, log, inc, result.Repo, sig, fix)
	}
	return res, nil
}

// graphOnlyDiagnosis summarises what the graph found when no patch is produced,
// so the incident still reaches a human with the analysis attached.
func graphOnlyDiagnosis(result *traverse.Result) string {
	if result == nil || len(result.Candidates) == 0 {
		return ""
	}
	top := result.Candidates[0]
	var b strings.Builder
	fmt.Fprintf(&b, "Graph traversal points at %s `%s`", strings.ToLower(top.Type), top.Name)
	if top.FilePath != "" {
		fmt.Fprintf(&b, " in %s", top.FilePath)
	}
	fmt.Fprintf(&b, " (score %.2f)", top.Score)
	if len(top.Reasons) > 0 {
		fmt.Fprintf(&b, " — %s", strings.Join(top.Reasons, "; "))
	}
	b.WriteString(".")
	return b.String()
}

// ship commits the changes and opens a pull request, recording the outcome on
// res. It also credits the changed files with a fault, raising them as suspects
// for the next incident in this part of the codebase.
func (r *Resolver) ship(
	ctx context.Context,
	log *slog.Logger,
	inc db.Incident,
	repo string,
	changes []patch.FileChange,
	res *db.Resolution,
	prBody string,
) {
	if payload, err := json.Marshal(changes); err == nil {
		res.Files = payload
	}

	paths := make([]string, 0, len(changes))
	for _, c := range changes {
		paths = append(paths, c.Path)
	}
	if err := r.store.RecordFaults(ctx, repo, paths, time.Now()); err != nil {
		log.Warn("record faults", "err", err)
	}

	if !r.cfg.AutoPR {
		res.Status = "no_fix"
		res.Error = "a validated fix is ready but automatic pull requests are disabled (set QUISAC_AUTO_PR=true)"
		log.Info("fix validated but not opened; auto-PR disabled", "files", len(changes))
		return
	}

	branch := github.BranchName(r.cfg.BranchPrefix, inc.ID)
	res.Branch = branch

	files := make([]github.FileContent, 0, len(changes))
	for _, c := range changes {
		files = append(files, github.FileContent{Path: c.Path, Content: c.After})
	}

	subject := res.Summary
	if subject == "" {
		subject = fmt.Sprintf("fix incident %s", inc.ID)
	}
	title := "fix: " + subject

	pr, err := r.src.OpenPullRequest(ctx, inc.InstallationID, repo, github.PROptions{
		Branch:        branch,
		CommitMessage: fmt.Sprintf("%s\n\nResolves PagerDuty incident %s (%s).\n", title, inc.ID, inc.ServiceName),
		Title:         title,
		Body:          prBody,
		Files:         files,
		Draft:         r.cfg.DraftPR,
	})
	if err != nil {
		// The patch was validated before we got here; only publishing failed. That
		// is a fix waiting on a human — the same shape as auto-PR being off above.
		res.Status = "no_fix"
		res.Error = publishError(repo, err)
		log.Error("open pull request", "err", err, "repo", repo, "branch", branch)
		return
	}

	res.Status = "resolved"
	res.PRURL = pr.URL
	res.PRNumber = pr.Number
	log.Info("pull request opened", "url", pr.URL, "files", len(changes))
}

// publishError explains a fix that was validated but could not be opened. The
// engineer is looking at a correct patch and needs to know the App is missing a
// permission, not that the analysis is wrong.
func publishError(repo string, err error) string {
	if github.IsPermissionDenied(err) {
		return fmt.Sprintf(
			"The fix below is validated and ready, but the GitHub App is not allowed to write to %s. "+
				"Grant it `contents: write` and `pull_requests: write` in the App's repository permissions, "+
				"then accept the updated permissions on the installation and re-run the pipeline. (%v)",
			repo, err)
	}
	return fmt.Sprintf("a validated fix is ready but the pull request could not be opened: %v", err)
}

// recordPattern stores a shipped fix so the next occurrence of this error shape
// is answered from cache instead of the model.
func (r *Resolver) recordPattern(ctx context.Context, log *slog.Logger, inc db.Incident, repo string, sig patterns.Signature, fix *llm.Fix) {
	edits, err := json.Marshal(fix.Edits)
	if err != nil {
		log.Warn("marshal pattern edits", "err", err)
		return
	}
	id, err := r.store.SavePattern(ctx, db.Pattern{
		InstallationID: inc.InstallationID,
		Repo:           repo,
		Fingerprint:    sig.Fingerprint,
		Signature:      sig.Text,
		Tokens:         sig.Tokens,
		ServiceName:    inc.ServiceName,
		RootCause:      fix.RootCause,
		FixSummary:     fix.Summary,
		Edits:          edits,
	})
	if err != nil {
		log.Warn("save pattern", "err", err)
		return
	}
	log.Info("pattern recorded", "pattern", id, "fingerprint", sig.Fingerprint[:12])
}

// fetchFiles reads paths from the repository's default branch, returning what it
// read and what it left out — the model is told what is missing rather than
// reasoning over a set it believes is complete.
func (r *Resolver) fetchFiles(ctx context.Context, installationID, repo string, paths []string) (map[string]string, []string, error) {
	files := make(map[string]string, len(paths))
	var skipped []string

	for _, p := range paths {
		content, found, err := r.src.GetFileContent(ctx, installationID, repo, p, "")
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", p, err)
		}
		if !found {
			skipped = append(skipped, p+" (not found)")
			continue
		}
		if len(content) > r.cfg.MaxFileBytes {
			// Truncating is worse than omitting: the model would anchor an edit in
			// code it never saw the rest of.
			skipped = append(skipped, fmt.Sprintf("%s (%d KB, too large)", p, len(content)/1024))
			continue
		}
		files[p] = string(content)
	}
	return files, skipped, nil
}
