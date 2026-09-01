package github

import (
	"context"
	"fmt"
	"sync"

	"quisac/internal/core"

	"github.com/google/go-github/v75/github"
)

// churnCommits bounds how far back the seed looks. On a two-week recency
// half-life, older history barely moves a score and costs an API call each.
const churnCommits = 100

// churnConcurrency bounds parallel commit fetches; a burst of hundreds is how an
// integration gets throttled.
const churnConcurrency = 8

// RecentFileChurn summarises which files changed, how often, and how recently.
//
// GitHub only returns a file list on a single-commit fetch, so this costs one
// request per commit: treat it as background enrichment, not something to block on.
func (a *App) RecentFileChurn(ctx context.Context, installationID, fullName string, maxCommits int) ([]core.FileChurn, error) {
	owner, repo, ok := splitFullName(fullName)
	if !ok {
		return nil, fmt.Errorf("RecentFileChurn: fullName must be owner/repo, got %q", fullName)
	}
	if maxCommits <= 0 || maxCommits > churnCommits {
		maxCommits = churnCommits
	}

	client, err := a.installClient(installationID)
	if err != nil {
		return nil, fmt.Errorf("RecentFileChurn: %w", err)
	}

	commits, _, err := client.Repositories.ListCommits(ctx, owner, repo, &github.CommitsListOptions{
		ListOptions: github.ListOptions{PerPage: maxCommits},
	})
	if err != nil {
		return nil, fmt.Errorf("RecentFileChurn: list commits: %w", err)
	}
	if len(commits) > maxCommits {
		commits = commits[:maxCommits]
	}

	var (
		mu    sync.Mutex
		churn = map[string]*core.FileChurn{}
		wg    sync.WaitGroup
		sem   = make(chan struct{}, churnConcurrency)
	)

	for _, c := range commits {
		sha := c.GetSHA()
		if sha == "" {
			continue
		}
		// A merge's file list is the whole branch, and its commits are already in
		// the list — counting both would inflate every file in it.
		if len(c.Parents) > 1 {
			continue
		}
		when := c.GetCommit().GetCommitter().GetDate().Time

		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if ctx.Err() != nil {
				return
			}
			full, _, err := client.Repositories.GetCommit(ctx, owner, repo, sha, nil)
			if err != nil {
				return // best-effort: one unreadable commit must not fail the seed
			}

			mu.Lock()
			defer mu.Unlock()
			for _, f := range full.Files {
				path := f.GetFilename()
				if path == "" {
					continue
				}
				e, ok := churn[path]
				if !ok {
					e = &core.FileChurn{Path: path}
					churn[path] = e
				}
				e.Changes++
				if when.After(e.LastChangedAt) {
					e.LastChangedAt = when
				}
			}
		}()
	}
	wg.Wait()

	out := make([]core.FileChurn, 0, len(churn))
	for _, e := range churn {
		out = append(out, *e)
	}
	return out, nil
}
