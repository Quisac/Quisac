package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	sessionCookieName = "quisac_gh"
	sessionMaxAge     = 7 * 24 * time.Hour
)

var (
	errNoSession       = errors.New("github: no session cookie")
	errSessionTampered = errors.New("github: session signature mismatch")
)

// session is the data stored (signed, not encrypted) in the user's browser.
//
// It contains a live GitHub user access token, so it MUST be signed to
// prevent forgery — see sessionStore below. It is not encrypted: do not
// add secrets to this struct beyond what's already here without revisiting
// that decision.
type session struct {
	Token        string        `json:"token"`
	Installation *Installation `json:"installation"`
	SelectedRepo string        `json:"selected_repo,omitempty"`
}

// sessionStore signs and verifies session cookies with HMAC-SHA256.
type sessionStore struct {
	secret []byte
}

func newSessionStore(secret string) sessionStore {
	return sessionStore{secret: []byte(secret)}
}

// Save serializes sess, signs it, and sets it as a cookie on w.
func (s sessionStore) Save(w http.ResponseWriter, sess *session) error {
	payload, err := json.Marshal(sess)
	if err != nil {
		return err
	}

	sig := s.sign(payload)
	value := hex.EncodeToString(payload) + "." + hex.EncodeToString(sig)

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   int(sessionMaxAge.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// Load reads, verifies, and decodes the session cookie from r.
func (s sessionStore) Load(r *http.Request) (*session, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return nil, errNoSession
	}

	payloadHex, sigHex, ok := strings.Cut(cookie.Value, ".")
	if !ok {
		return nil, errSessionTampered
	}

	payload, err := hex.DecodeString(payloadHex)
	if err != nil {
		return nil, errSessionTampered
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return nil, errSessionTampered
	}

	if !hmac.Equal(sig, s.sign(payload)) {
		return nil, errSessionTampered
	}

	var sess session
	if err := json.Unmarshal(payload, &sess); err != nil {
		return nil, errSessionTampered
	}
	return &sess, nil
}

// Clear removes the session cookie.
func (s sessionStore) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:   sessionCookieName,
		Path:   "/",
		MaxAge: -1,
	})
}

func (s sessionStore) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write(payload)
	return mac.Sum(nil)
}
