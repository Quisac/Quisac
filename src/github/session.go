package github

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
)

const (
	stateCookie   = "quisac_oauth_state"
	sessionCookie = "quisac_gh"
)

func setStateCookie(w http.ResponseWriter, state string) {
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookie,
		Value:    state,
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:   stateCookie,
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
}

func saveSession(w http.ResponseWriter, token string, inst *Installation) error {
	b, err := json.Marshal(session{
		Token:        token,
		Installation: inst,
	})
	if err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    hex.EncodeToString(b),
		Path:     "/",
		MaxAge:   60 * 60 * 24 * 7,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	return nil
}
