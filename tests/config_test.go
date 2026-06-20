package tests

import (
	"testing"

	"quisac/src/github"
)

func TestLoadConfig(t *testing.T) {
	t.Setenv("GITHUB_APP_SLUG", "quisac")
	t.Setenv("GITHUB_CLIENT_ID", "client-id")
	t.Setenv("GITHUB_CLIENT_SECRET", "client-secret")
	t.Setenv("GITHUB_REDIRECT_URL", "http://localhost:8000/github/callback")

	cfg, err := github.LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.AppSlug != "quisac" {
		t.Fatalf("expected quisac got %s", cfg.AppSlug)
	}

	if cfg.ClientID != "client-id" {
		t.Fatalf("expected client-id")
	}
}

func TestLoadConfigMissing(t *testing.T) {
	t.Setenv("GITHUB_APP_SLUG", "")
	t.Setenv("GITHUB_CLIENT_ID", "")
	t.Setenv("GITHUB_CLIENT_SECRET", "")
	t.Setenv("GITHUB_REDIRECT_URL", "")

	_, err := github.LoadConfig()

	if err == nil {
		t.Fatal("expected error")
	}
}
