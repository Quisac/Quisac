package github_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/url"
	"testing"

	"quisac/internal/config"
	"quisac/internal/github"
)

// testPrivateKey generates a throwaway RSA key in PEM form so github.New can
// build its app transport without reaching out to GitHub.
func testPrivateKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
}

func TestInstallURL(t *testing.T) {
	cfg := config.GitHub{AppSlug: "quisac"}
	cfg.App.IntegrationID = 1234
	cfg.App.PrivateKey = testPrivateKey(t)

	app, err := github.New(cfg)
	if err != nil {
		t.Fatalf("github.New: %v", err)
	}

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
