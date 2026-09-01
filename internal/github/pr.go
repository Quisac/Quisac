package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/go-github/v75/github"
)

// PullRequest is an opened pull request, as returned to the incident pipeline.
type PullRequest struct {
	Number    int    `json:"number"`
	URL       string `json:"url"`
	Branch    string `json:"branch"`
	CommitSHA string `json:"commit_sha"`
}

// FileContent is one file's complete new contents, at its repo-relative path.
type FileContent struct {
	Path    string
	Content string
}

// PROptions describes the branch, commit, and pull request to open.
type PROptions struct {
	Branch        string        // branch to create; must not be the base branch
	Base          string        // base branch; empty means the repo's default
	CommitMessage string        // full commit message (subject, blank line, body)
	Title         string        // pull request title
	Body          string        // pull request body, Markdown
	Files         []FileContent // complete new contents of every changed file
	Draft         bool          // open as a draft
}

// OpenPullRequest commits the supplied file contents to a new branch and opens a
// pull request against the base branch. Requires the App to hold
// `contents: write` and `pull_requests: write`.
//
// The commit goes through the Git data API rather than the contents API so every
// file lands in one atomic commit; file-by-file writes would leave the branch
// half-fixed if a later one failed.
func (a *App) OpenPullRequest(ctx context.Context, installationID, fullName string, opts PROptions) (*PullRequest, error) {
	owner, repo, ok := splitFullName(fullName)
	if !ok {
		return nil, fmt.Errorf("OpenPullRequest: fullName must be owner/repo, got %q", fullName)
	}
	if len(opts.Files) == 0 {
		return nil, fmt.Errorf("OpenPullRequest: no files to commit")
	}
	if opts.Branch == "" {
		return nil, fmt.Errorf("OpenPullRequest: branch is required")
	}

	client, err := a.installClient(installationID)
	if err != nil {
		return nil, fmt.Errorf("OpenPullRequest: %w", err)
	}

	base := opts.Base
	if base == "" {
		r, _, err := client.Repositories.Get(ctx, owner, repo)
		if err != nil {
			return nil, fmt.Errorf("OpenPullRequest: get repo: %w", err)
		}
		base = r.GetDefaultBranch()
	}
	if opts.Branch == base {
		return nil, fmt.Errorf("OpenPullRequest: refusing to commit directly to the base branch %q", base)
	}

	// Anchor the commit to the current tip of the base branch.
	baseRef, _, err := client.Git.GetRef(ctx, owner, repo, "refs/heads/"+base)
	if err != nil {
		return nil, fmt.Errorf("OpenPullRequest: get base ref %q: %w", base, err)
	}
	baseSHA := baseRef.GetObject().GetSHA()

	baseCommit, _, err := client.Git.GetCommit(ctx, owner, repo, baseSHA)
	if err != nil {
		return nil, fmt.Errorf("OpenPullRequest: get base commit: %w", err)
	}

	// A tree entry carrying Content has GitHub create the blob, saving a round trip.
	entries := make([]*github.TreeEntry, 0, len(opts.Files))
	for _, f := range opts.Files {
		content := f.Content
		entries = append(entries, &github.TreeEntry{
			Path:    github.Ptr(f.Path),
			Mode:    github.Ptr("100644"),
			Type:    github.Ptr("blob"),
			Content: github.Ptr(content),
		})
	}

	tree, _, err := client.Git.CreateTree(ctx, owner, repo, baseCommit.GetTree().GetSHA(), entries)
	if err != nil {
		return nil, fmt.Errorf("OpenPullRequest: create tree: %w", err)
	}

	commit, _, err := client.Git.CreateCommit(ctx, owner, repo, github.Commit{
		Message: github.Ptr(opts.CommitMessage),
		Tree:    tree,
		Parents: []*github.Commit{{SHA: github.Ptr(baseSHA)}},
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("OpenPullRequest: create commit: %w", err)
	}
	commitSHA := commit.GetSHA()

	if err := upsertBranch(ctx, client, owner, repo, opts.Branch, commitSHA); err != nil {
		return nil, err
	}

	pr, _, err := client.PullRequests.Create(ctx, owner, repo, &github.NewPullRequest{
		Title: github.Ptr(opts.Title),
		Head:  github.Ptr(opts.Branch),
		Base:  github.Ptr(base),
		Body:  github.Ptr(opts.Body),
		Draft: github.Ptr(opts.Draft),
		// The fix is a proposal for the on-call engineer; let them push to it.
		MaintainerCanModify: github.Ptr(true),
	})
	if err != nil {
		// The commit exists, so the work is not lost; the caller must know there
		// is no PR over it.
		return nil, fmt.Errorf("OpenPullRequest: create pull request (branch %q was pushed): %w", opts.Branch, err)
	}

	return &PullRequest{
		Number:    pr.GetNumber(),
		URL:       pr.GetHTMLURL(),
		Branch:    opts.Branch,
		CommitSHA: commitSHA,
	}, nil
}

// IsPermissionDenied reports whether GitHub refused the App a scope, rather than
// a transport or repository problem. An under-permissioned App gets 403
// "Resource not accessible by integration" on its first write, the tree creation.
func IsPermissionDenied(err error) bool {
	var errResp *github.ErrorResponse
	if errors.As(err, &errResp) && errResp.Response != nil {
		return errResp.Response.StatusCode == http.StatusForbidden
	}
	return false
}

// upsertBranch points branch at sha, creating the ref or moving an existing one.
// A retry whose worker died after pushing but before opening the PR finds its own
// leftover branch, and forcing the ref lets it converge instead of failing.
func upsertBranch(ctx context.Context, client *github.Client, owner, repo, branch, sha string) error {
	ref := "refs/heads/" + branch
	_, resp, err := client.Git.CreateRef(ctx, owner, repo, github.CreateRef{Ref: ref, SHA: sha})
	if err == nil {
		return nil
	}
	// 422 is GitHub's "reference already exists".
	if resp == nil || resp.StatusCode != http.StatusUnprocessableEntity {
		return fmt.Errorf("OpenPullRequest: create branch %q: %w", branch, err)
	}
	if _, _, err := client.Git.UpdateRef(ctx, owner, repo, ref, github.UpdateRef{
		SHA:   sha,
		Force: github.Ptr(true),
	}); err != nil {
		return fmt.Errorf("OpenPullRequest: update existing branch %q: %w", branch, err)
	}
	return nil
}

// BranchName builds a valid git ref: lowercase, no runs of separators, bounded.
func BranchName(prefix, incidentID string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '-'
		}
	}, incidentID)

	for strings.Contains(clean, "--") {
		clean = strings.ReplaceAll(clean, "--", "-")
	}
	clean = strings.Trim(clean, "-")
	if len(clean) > 40 {
		clean = strings.Trim(clean[:40], "-")
	}
	if clean == "" {
		clean = "incident"
	}
	return prefix + "/" + clean
}
