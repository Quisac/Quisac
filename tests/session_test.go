package tests

import (
	"net/http/httptest"
	"testing"

	"quisac/src/github"
)

func TestSetStateCookie(t *testing.T) {
	rr := httptest.NewRecorder()

	github.SetStateCookie(rr, "abc123")

	resp := rr.Result()

	cookies := resp.Cookies()

	if len(cookies) == 0 {
		t.Fatal("expected cookie")
	}

	cookie := cookies[0]

	if cookie.Name != github.StateCookieName {
		t.Fatalf("unexpected cookie name: %s", cookie.Name)
	}

	if cookie.Value != "abc123" {
		t.Fatalf("unexpected cookie value: %s", cookie.Value)
	}
}

func TestClearStateCookie(t *testing.T) {
	rr := httptest.NewRecorder()

	github.ClearStateCookie(rr)

	resp := rr.Result()

	cookies := resp.Cookies()

	if len(cookies) == 0 {
		t.Fatal("expected cookie")
	}

	if cookies[0].MaxAge != -1 {
		t.Fatal("expected deleted cookie")
	}
}

func TestSaveSession(t *testing.T) {
	rr := httptest.NewRecorder()

	inst := &github.Installation{
		ID: 123,
	}

	err := github.SaveSession(
		rr,
		"token123",
		inst,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp := rr.Result()

	cookies := resp.Cookies()

	if len(cookies) == 0 {
		t.Fatal("expected session cookie")
	}

	if cookies[0].Name != github.SessionCookieName {
		t.Fatalf("unexpected cookie name")
	}
}
