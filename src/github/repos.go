package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Repo is the subset of a GitHub repository the dashboard needs to render the
// repository picker.
type Repo struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	HTMLURL       string `json:"html_url"`
	DefaultBranch string `json:"default_branch"`
}

// installationToken exchanges the app JWT for a short-lived installation
// access token, which is scoped to exactly the repositories the user granted.
func (a *App) installationToken(ctx context.Context, installationID string) (string, error) {
	jwt, err := a.appJWT()
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("https://api.github.com/app/installations/%s/access_tokens", installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := a.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("github: installation token returned %s", resp.Status)
	}

	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.Token, nil
}

// Repositories lists every repository the installation can access, following
// pagination so all selected repos are returned.
func (a *App) Repositories(ctx context.Context, installationID string) ([]Repo, error) {
	token, err := a.installationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}

	var repos []Repo
	for page := 1; ; page++ {
		batch, total, err := a.reposPage(ctx, token, page)
		if err != nil {
			return nil, err
		}
		repos = append(repos, batch...)
		if len(repos) >= total || len(batch) == 0 {
			break
		}
	}
	return repos, nil
}

// reposPage fetches one page (up to 100) of installation repositories and the
// reported total count.
func (a *App) reposPage(ctx context.Context, token string, page int) ([]Repo, int, error) {
	url := fmt.Sprintf("https://api.github.com/installation/repositories?per_page=100&page=%d", page)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := a.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("github: list repositories returned %s", resp.Status)
	}

	var body struct {
		TotalCount   int    `json:"total_count"`
		Repositories []Repo `json:"repositories"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, 0, err
	}
	return body.Repositories, body.TotalCount, nil
}
