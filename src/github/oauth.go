package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// AuthorizeURL returns GitHub's OAuth authorize URL. Unlike the install URL,
// this is clean for returning users
func (a *App) AuthorizeURL(state string) string {
	u := url.URL{Scheme: "https", Host: "github.com", Path: "/login/oauth/authorize"}
	q := u.Query()
	q.Set("client_id", a.clientID)
	q.Set("redirect_uri", a.redirectURL)
	q.Set("state", state)
	u.RawQuery = q.Encode()
	return u.String()
}

// Installation identifies one installation of the app on a user/org account.
type Installation struct {
	ID      int64 `json:"id"`
	Account struct {
		Login string `json:"login"`
	} `json:"account"`
}

// UserInstallations lists the installations of this app that the user (via
// their OAuth token) can access. An empty result means the app is not yet
// installed for them.
func (a *App) UserInstallations(ctx context.Context, userToken string) ([]Installation, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/user/installations?per_page=100", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+userToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github: GET /user/installations returned %s", resp.Status)
	}

	var body struct {
		Installations []Installation `json:"installations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.Installations, nil
}

// ExchangeCode trades the OAuth code returned by the install + authorize flow
// for a user access token. The token identifies the human who connected and
// confirms they completed authorization.
func (a *App) ExchangeCode(ctx context.Context, code string) (string, error) {
	form := url.Values{
		"client_id":     {a.clientID},
		"client_secret": {a.clientSecret},
		"code":          {code},
		"redirect_uri":  {a.redirectURL},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://github.com/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := a.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github: token exchange returned %s", resp.Status)
	}

	var body struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("github: token exchange failed: %s", body.Error)
	}
	return body.AccessToken, nil
}

// User returns the login of the user who owns the given access token.
func (a *App) User(ctx context.Context, userToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/user", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+userToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := a.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github: GET /user returned %s", resp.Status)
	}

	var body struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.Login, nil
}
