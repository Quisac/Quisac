// Package server wires the HTTP routes: the static frontend and the GitHub
// App connect flow.
package server

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"quisac/src/config"
	"quisac/src/github"
	"quisac/src/session"
)

const oauthStateCookie = "quisac_oauth_state"

// Server holds the dependencies shared across HTTP handlers.
type Server struct {
	cfg    *config.Config
	app    *github.App
	signer *session.Signer
}

// New builds a Server from a resolved config.
func New(cfg *config.Config) *Server {
	return &Server{
		cfg:    cfg,
		app:    github.New(cfg.GitHub),
		signer: session.NewSigner(cfg.Server.SessionSecret),
	}
}

// Handler returns the router with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/connect", s.handleConnect)
	mux.HandleFunc("/", s.handleStatic)
	return mux
}

// ListenAndServe starts the HTTP server on the configured port.
func (s *Server) ListenAndServe() error {
	port := s.cfg.Server.Port
	fmt.Printf("serving → http://localhost:%s\n", port)
	return http.ListenAndServe(":"+port, s.Handler())
}

// handleConnect starts the GitHub App install + authorize flow. It generates
// an unguessable state, stores it in a signed, short-lived cookie, and
// redirects the browser to GitHub's installation consent screen. The callback
// later checks the returned state against this cookie to defend against CSRF.
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	state, err := randomState()
	if err != nil {
		http.Error(w, "could not start connect flow", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    s.signer.Sign(state),
		Path:     "/",
		MaxAge:   600, // 10 minutes to complete the flow
		HttpOnly: true,
		Secure:   s.secureCookies(),
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, s.app.InstallURL(state), http.StatusFound)
}

// handleStatic serves the built frontend from html/dist.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, ".js") {
		w.Header().Set("Content-Type", "application/javascript")
	} else if strings.HasSuffix(r.URL.Path, ".css") {
		w.Header().Set("Content-Type", "text/css")
	}
	http.FileServer(http.Dir("html/dist")).ServeHTTP(w, r)
}

// secureCookies reports whether cookies should carry the Secure flag, i.e.
// when the app is served over HTTPS (everywhere except local http).
func (s *Server) secureCookies() bool {
	return strings.HasPrefix(s.cfg.GitHub.RedirectURL, "https://")
}

// randomState returns 32 bytes of base64url-encoded randomness.
func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
