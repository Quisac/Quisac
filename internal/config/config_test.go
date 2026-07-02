package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeEnv(t *testing.T, body string) {
	t.Helper()

	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Chdir(dir)
}

func TestLoadReadsEnvFile(t *testing.T) {
	key := "-----BEGIN RSA PRIVATE KEY-----\nLINE1\nLINE2\n-----END RSA PRIVATE KEY-----"

	writeEnv(t, `
GITHUB_APP_SLUG=quisac
GITHUB_APP_ID=12345
GITHUB_REDIRECT_URL=http://localhost:8000/callback
GITHUB_CLIENT_ID=test-client
GITHUB_CLIENT_SECRET=test-secret
QUISAC_SESSION_SECRET=session-secret
DATABASE_URL=postgres://localhost:5432/quisac
GITHUB_APP_PRIVATE_KEY="`+key+`"
PORT=8000
`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.GitHub.AppSlug != "quisac" {
		t.Errorf("AppSlug = %q, want quisac", cfg.GitHub.AppSlug)
	}

	if cfg.GitHub.App.IntegrationID != 12345 {
		t.Errorf("IntegrationID = %d, want 12345", cfg.GitHub.App.IntegrationID)
	}

	if cfg.Server.Port != "8000" {
		t.Errorf("Port = %q, want 8000", cfg.Server.Port)
	}

	if cfg.GitHub.App.PrivateKey != key {
		t.Errorf("private key mismatch:\n got %q\nwant %q",
			cfg.GitHub.App.PrivateKey,
			key,
		)
	}

	if !strings.HasPrefix(cfg.GitHub.App.PrivateKey, "-----BEGIN") {
		t.Errorf("private key lost PEM header")
	}
}

func TestLoadDoesNotOverrideRealEnv(t *testing.T) {
	writeEnv(t, `
GITHUB_APP_SLUG=from-file
GITHUB_APP_ID=12345
GITHUB_REDIRECT_URL=http://localhost:8000/callback
GITHUB_CLIENT_ID=file-client
GITHUB_CLIENT_SECRET=file-secret
QUISAC_SESSION_SECRET=file-session
DATABASE_URL=postgres://localhost:5432/quisac
`)

	os.Setenv("GITHUB_APP_SLUG", "from-real-env")
	t.Cleanup(func() {
		os.Unsetenv("GITHUB_APP_SLUG")
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.GitHub.AppSlug != "from-real-env" {
		t.Errorf("real env should win: got %q", cfg.GitHub.AppSlug)
	}
}

func TestLoadValidatesRequiredEnv(t *testing.T) {
	writeEnv(t, ``)

	_, err := Load()
	if err == nil {
		t.Fatal("expected validation error")
	}

	if !strings.Contains(err.Error(), "GITHUB_APP_SLUG") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoadUsesDefaultPort(t *testing.T) {
	writeEnv(t, `
GITHUB_APP_SLUG=quisac
GITHUB_APP_ID=12345
GITHUB_REDIRECT_URL=http://localhost:8000/callback
GITHUB_CLIENT_ID=test-client
GITHUB_CLIENT_SECRET=test-secret
QUISAC_SESSION_SECRET=session-secret
DATABASE_URL=postgres://localhost:5432/quisac
`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Server.Port != "8000" {
		t.Errorf("default port = %q, want 8000", cfg.Server.Port)
	}
}
