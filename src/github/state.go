package github

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
)

// errStateMismatch is returned when the state parameter on an OAuth
// callback doesn't match what was generated for that flow.
var errStateMismatch = errors.New("github: oauth state mismatch")

// StateStore generates and verifies the state parameter used to guard the
// OAuth2 flow against CSRF. The interface exists so the storage mechanism
// (cookie, server-side session, ...) can change without touching the
// handlers that drive the OAuth flow.
//
// This mirrors the StateStore pattern from palantir/go-githubapp's oauth2
// package: https://github.com/palantir/go-githubapp/blob/develop/oauth2/state.go
type StateStore interface {
	// GenerateState creates a new state value and stores it somewhere it
	// can be retrieved by VerifyState on the later callback request.
	GenerateState(w http.ResponseWriter, r *http.Request) (string, error)

	// VerifyState checks that the state on the callback request matches
	// the one GenerateState produced. Implementations should use a
	// constant-time comparison to avoid leaking the expected value through
	// response-timing side channels.
	VerifyState(r *http.Request, state string) (bool, error)
}

// cookieStateStore is the default StateStore: it round-trips the state
// value through a short-lived, HttpOnly cookie. This is sufficient for a
// single-instance deployment with no shared session store; if Quisac later
// needs state to survive across instances behind a load balancer, swap in
// a server-side-session-backed StateStore without changing any handler code.
type cookieStateStore struct{}

const (
	oauthStateCookie = "quisac_oauth_state"
	oauthStateMaxAge = 300 // seconds
)

func (cookieStateStore) GenerateState(w http.ResponseWriter, r *http.Request) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	state := hex.EncodeToString(b)

	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    state,
		Path:     "/",
		MaxAge:   oauthStateMaxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	return state, nil
}

func (cookieStateStore) VerifyState(r *http.Request, expected string) (bool, error) {
	cookie, err := r.Cookie(oauthStateCookie)
	if err != nil {
		return false, nil // no cookie is a verification failure, not a hard error
	}
	if cookie.Value == "" || expected == "" {
		return false, nil
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(expected)) == 1, nil
}

// clearOAuthStateCookie removes the state cookie once the flow completes
// (success or failure), so it can't be replayed.
func clearOAuthStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: oauthStateCookie, Path: "/", MaxAge: -1})
}
