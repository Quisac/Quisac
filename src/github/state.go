package github

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
)

const (
	oauthStateCookie = "quisac_oauth_state"
	oauthStateMaxAge = 300 // seconds
)

var errStateMismatch = errors.New("github: oauth state mismatch")

// newOAuthState generates a random CSRF token for the OAuth flow.
func newOAuthState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func setOAuthStateCookie(w http.ResponseWriter, state string) {
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    state,
		Path:     "/",
		MaxAge:   oauthStateMaxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearOAuthStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: oauthStateCookie, Path: "/", MaxAge: -1})
}

// verifyOAuthState checks the state param on r against the cookie set by
// setOAuthStateCookie, guarding against CSRF on the OAuth callback.
func verifyOAuthState(r *http.Request) error {
	cookie, err := r.Cookie(oauthStateCookie)
	if err != nil {
		return errStateMismatch
	}
	if cookie.Value == "" || cookie.Value != r.URL.Query().Get("state") {
		return errStateMismatch
	}
	return nil
}
