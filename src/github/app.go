// Package github drives the GitHub App install + authorize flow: sending the
// user to GitHub's consent screen, handling the callback, and (later) listing
// the repositories the user granted access to.
package github

import (
	"net/http"
	"net/url"
	"time"

	"quisac/src/config"
)

// App holds the GitHub App credentials and settings needed to authenticate
// and to drive the install + authorize flow.
type App struct {
	slug        string
	redirectURL string

	appID         int64
	privateKey    string
	webhookSecret string

	clientID     string
	clientSecret string

	http *http.Client
}

// New builds an App from the GitHub section of the config.
func New(cfg config.GitHub) *App {
	return &App{
		slug:          cfg.AppSlug,
		redirectURL:   cfg.RedirectURL,
		appID:         cfg.App.IntegrationID,
		privateKey:    cfg.App.PrivateKey,
		webhookSecret: cfg.App.WebhookSecret,
		clientID:      cfg.OAuth.ClientID,
		clientSecret:  cfg.OAuth.ClientSecret,
		http:          &http.Client{Timeout: 10 * time.Second},
	}
}

// InstallURL returns the URL of GitHub's app installation screen — the one
// that lets the user choose "All repositories" or "Only select repositories"
// (the Vercel-style picker). When the app has "Request user authorization
// (OAuth) during installation" enabled, completing this flow also returns an
// OAuth code to the callback, giving us the user's identity in one redirect.
//
// state is an opaque, unguessable value echoed back to the callback; it is
// used to defend against CSRF.
func (a *App) InstallURL(state string) string {
	u := url.URL{
		Scheme: "https",
		Host:   "github.com",
		Path:   "/apps/" + a.slug + "/installations/new",
	}
	q := u.Query()
	q.Set("state", state)
	u.RawQuery = q.Encode()
	return u.String()
}
