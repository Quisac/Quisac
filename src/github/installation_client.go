package github

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// tokenRefreshSkew re-mints an installation token this long before its
// reported expiry so in-flight requests don't race a token that expires
// mid-call.
const tokenRefreshSkew = 2 * time.Minute

// installationClient is a GitHub API client authenticated as a specific
// app installation (not a user). It mints its own access token via the
// app's JWT and refreshes it automatically when it is close to expiring.
//
// Use this for requests Quisac makes on its own behalf — reacting to a
// webhook, opening a PR as the app — where there is no logged-in user
// session to borrow a token from. For requests made while a user is
// actively in the OAuth flow, use client instead.
type installationClient struct {
	appID          int64
	installationID int64
	privateKey     *rsa.PrivateKey
	apiBase        string // e.g. "https://api.github.com"
	webBase        string // e.g. "https://github.com" — used to mint tokens

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

// newInstallationClient builds a client authenticated as the given
// installation of the app described by cfg. cfg must pass ValidateAppAuth.
func newInstallationClient(cfg Config, installationID int64) (*installationClient, error) {
	if err := cfg.ValidateAppAuth(); err != nil {
		return nil, err
	}
	key, err := parsePrivateKey(cfg.PrivateKeyPEM)
	if err != nil {
		return nil, err
	}
	return &installationClient{
		appID:          cfg.AppID,
		installationID: installationID,
		privateKey:     key,
		apiBase:        cfg.apiBaseURL(),
		webBase:        cfg.webBaseURL(),
	}, nil
}

// accessToken returns a valid installation access token, minting or
// refreshing one if the cached token is missing or close to expiry.
// Safe for concurrent use.
func (c *installationClient) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && time.Now().Before(c.expiresAt.Add(-tokenRefreshSkew)) {
		return c.token, nil
	}

	token, expiresAt, err := c.mintToken(ctx)
	if err != nil {
		return "", err
	}
	c.token = token
	c.expiresAt = expiresAt
	return c.token, nil
}

// mintToken requests a fresh installation access token from GitHub,
// authenticating the request with a freshly-signed app JWT.
func (c *installationClient) mintToken(ctx context.Context) (token string, expiresAt time.Time, err error) {
	appJWT, err := signAppJWT(c.appID, c.privateKey, time.Now())
	if err != nil {
		return "", time.Time{}, fmt.Errorf("github: sign app jwt: %w", err)
	}

	// GitHub Enterprise Server uses /api/v3/app/installations/... ;
	// apiBase already has the right prefix regardless of the host.
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.apiBase, c.installationID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("github: build installation token request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVer)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("github: installation token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", time.Time{}, fmt.Errorf("github: mint installation token: %s", resp.Status)
	}

	var body struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", time.Time{}, fmt.Errorf("github: decode installation token response: %w", err)
	}
	if body.Token == "" {
		return "", time.Time{}, fmt.Errorf("github: empty token in installation token response")
	}

	return body.Token, body.ExpiresAt, nil
}

// get issues an authenticated GET to the GitHub API as this installation,
// minting or refreshing the access token as needed, and decodes the JSON
// response body into out. Mirrors client.get's shape and error conventions.
func (c *installationClient) get(ctx context.Context, path string, out any) error {
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+path, nil)
	if err != nil {
		return fmt.Errorf("github: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
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
