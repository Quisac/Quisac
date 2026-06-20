package tests

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"quisac/src/github"
)

func TestConnectHandler(t *testing.T) {
	cfg := github.Config{
		AppSlug: "quisac",
	}

	service := github.NewService(cfg)

	handler := github.NewHandler(service)

	mux := http.NewServeMux()

	handler.Register(mux)

	req := httptest.NewRequest(
		http.MethodGet,
		"/github/connect",
		nil,
	)

	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf(
			"expected status %d got %d",
			http.StatusFound,
			rr.Code,
		)
	}

	location := rr.Header().Get("Location")

	if location == "" {
		t.Fatal("expected redirect location")
	}
}

func TestCallbackMissingState(t *testing.T) {
	service := github.NewService(
		github.Config{},
	)

	handler := github.NewHandler(service)

	req := httptest.NewRequest(
		http.MethodGet,
		"/github/callback",
		nil,
	)

	rr := httptest.NewRecorder()

	handler.Callback(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf(
			"expected %d got %d",
			http.StatusBadRequest,
			rr.Code,
		)
	}
}
