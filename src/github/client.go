package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	apiVer         = "2022-11-28"
	requestTimeout = 10 * time.Second
)

// httpClient is shared by every outbound call this package makes.
// http.Client is safe for concurrent use; there is no per-request state
// that needs isolating between callers.
var httpClient = &http.Client{Timeout: requestTimeout}

// Installation is a GitHub App installation.
type Installation struct {
	ID      int64 `json:"id"`
	Account struct {
		Login string `json:"login"`
		Type  string `json:"type"` // "User" | "Organization"
	} `json:"account"`
}

// Repo is a GitHub repository.
type Repo struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Private  bool   `json:"private"`
	Language string `json:"language"`
}

// client is a minimal GitHub API client authenticated with a user token.
type client struct {
	token   string
	apiBase string // e.g. "https://api.github.com"
}

func newClient(token string, cfg Config) *client {
	return &client{
		token:   token,
		apiBase: cfg.apiBaseURL(),
	}
}

// get issues an authenticated GET to the GitHub API and decodes the JSON
// response body into out.
func (c *client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+path, nil)
	if err != nil {
		return fmt.Errorf("github: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVer)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("github: GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("github: GET %s: %s", path, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("github: decode response for %s: %w", path, err)
	}
	return nil
}

// FindInstallation returns the installation matching id among those
// accessible to the authenticated user.
//
// GitHub has no GET /user/installations/:id endpoint, so this lists all
// installations and filters client-side. For typical per-user installation
// counts this is one cheap request; it's not designed to scale to users
// with hundreds of installations.
func (c *client) FindInstallation(ctx context.Context, id int64) (*Installation, error) {
	var body struct {
		Installations []Installation `json:"installations"`
	}
	if err := c.get(ctx, "/user/installations?per_page=100", &body); err != nil {
		return nil, err
	}
	for i := range body.Installations {
		if body.Installations[i].ID == id {
			return &body.Installations[i], nil
		}
	}
	return nil, fmt.Errorf("github: installation %d not found", id)
}

// ListRepos returns repositories accessible to an installation.
func (c *client) ListRepos(ctx context.Context, installationID int64) ([]Repo, error) {
	path := fmt.Sprintf("/user/installations/%d/repositories?per_page=100", installationID)
	var body struct {
		Repositories []Repo `json:"repositories"`
	}
	if err := c.get(ctx, path, &body); err != nil {
		return nil, err
	}
	return body.Repositories, nil
}

// exchangeCode trades an OAuth code for a user access token.
func exchangeCode(ctx context.Context, cfg Config, code string) (string, error) {
	tokenURL := cfg.webBaseURL() + "/login/oauth/access_token"

	params := url.Values{
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
		"code":          {code},
		"redirect_uri":  {cfg.RedirectURL},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, nil)
	if err != nil {
		return "", fmt.Errorf("github: build token request: %w", err)
	}
	req.URL.RawQuery = params.Encode()
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github: token exchange request: %w", err)
	}
	defer resp.Body.Close()

	// Read the full body so we can log it on error without a second read.
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if err != nil {
		return "", fmt.Errorf("github: read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github: token exchange: %s: %s", resp.Status, respBody)
	}

	var body struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Scope       string `json:"scope"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(respBody, &body); err != nil {
		return "", fmt.Errorf("github: decode token response: %w (raw: %s)", err, respBody)
	}
	if body.Error != "" {
		return "", fmt.Errorf("github: token exchange: %s: %s", body.Error, body.ErrorDesc)
	}
	if body.AccessToken == "" {
		// Log the full raw response so we can see what GitHub actually sent.
		return "", fmt.Errorf("github: token exchange: empty access_token (scope=%q raw=%s)", body.Scope, respBody)
	}
	return body.AccessToken, nil
}
