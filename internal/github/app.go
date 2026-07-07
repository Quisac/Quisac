package github

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"golang.org/x/oauth2"
	githuboauth "golang.org/x/oauth2/github"

	"quisac/internal/config"
)

type App struct {
	slug          string
	appsTransport *ghinstallation.AppsTransport
	oauth         *oauth2.Config
	http          *http.Client
}

func New(cfg config.GitHub) (*App, error) {
	atr, err := ghinstallation.NewAppsTransport(
		http.DefaultTransport,
		cfg.App.IntegrationID,
		[]byte(cfg.App.PrivateKey),
	)
	if err != nil {
		return nil, fmt.Errorf("github: parse app private key: %w", err)
	}

	return &App{
		slug:          cfg.AppSlug,
		appsTransport: atr,
		oauth: &oauth2.Config{
			ClientID:     cfg.OAuth.ClientID,
			ClientSecret: cfg.OAuth.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     githuboauth.Endpoint,
		},
		http: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

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
