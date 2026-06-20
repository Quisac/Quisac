package github

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
)

const (
	StateCookieName   = "quisac_oauth_state"
	SessionCookieName = "quisac_gh"
)

func SetStateCookie(w http.ResponseWriter, state string) {
	http.SetCookie(w, &http.Cookie{
		Name:     StateCookieName,
		Value:    state,
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func ClearStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:   StateCookieName,
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
}

func SaveSession(
	w http.ResponseWriter,
	token string,
	inst *Installation,
) error {
	data, err := json.Marshal(Session{
		Token:        token,
		Installation: inst,
	})
	if err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    hex.EncodeToString(data),
		Path:     "/",
		MaxAge:   60 * 60 * 24 * 7,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	return nil
}
