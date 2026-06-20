package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"quisac/src/github"
)

func TestInstallURL(t *testing.T) {
	cfg := github.Config{
		AppSlug: "quisac",
	}

	service := github.NewService(cfg)

	url := service.InstallURL("state123")

	expected := "https://github.com/apps/quisac/installations/new?state=state123"

	if url != expected {
		t.Fatalf("expected %s got %s", expected, url)
	}
}

func TestRandomState(t *testing.T) {
	state, err := github.RandomState()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(state) == 0 {
		t.Fatal("expected non-empty state")
	}
}

func TestFindInstallation(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := map[string]any{
				"installations": []map[string]any{
					{
						"id": 123,
						"account": map[string]any{
							"login": "quisac",
						},
					},
				},
			}

			json.NewEncoder(w).Encode(resp)
		}),
	)

	defer server.Close()

	old := github.GitHubAPIURL
	github.GitHubAPIURL = server.URL

	defer func() {
		github.GitHubAPIURL = old
	}()

	service := github.NewService(
		github.Config{},
	)

	inst, err := service.FindInstallation(
		"token",
		"123",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if inst.Account.Login != "quisac" {
		t.Fatalf("expected acme got %s", inst.Account.Login)
	}
}
