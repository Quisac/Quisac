package tests

import (
	"net/url"
	"testing"

	"quisac/src/config"
	"quisac/src/github"
)

func TestInstallURL(t *testing.T) {
	app := github.New(config.GitHub{AppSlug: "quisac"})

	raw := app.InstallURL("xyz state/with+chars")

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("InstallURL produced invalid URL %q: %v", raw, err)
	}
	if u.Host != "github.com" {
		t.Errorf("host = %q, want github.com", u.Host)
	}
	if u.Path != "/apps/quisac/installations/new" {
		t.Errorf("path = %q, want /apps/quisac/installations/new", u.Path)
	}
	if got := u.Query().Get("state"); got != "xyz state/with+chars" {
		t.Errorf("state = %q, want the original (decoded) value", got)
	}
}
