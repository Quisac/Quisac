// Package server wires the HTTP routes: the static frontend and the GitHub
// App connect flow.
package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"quisac/src/config"
	"quisac/src/github"
	"quisac/src/session"
)

const (
	oauthStateCookie = "quisac_oauth_state"
	sessionCookie    = "quisac_session"
)

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
	mux.HandleFunc("/github/callback", s.handleCallback)
	mux.HandleFunc("/api/repos", s.handleRepos)
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

// handleCallback completes the install + authorize flow. GitHub redirects here
// (the app's Callback URL, /github/callback) with a state, an OAuth code, and
// the installation_id. We verify state against
// the signed cookie (CSRF), exchange the code to confirm the user authorized,
// and store the installation_id in a session cookie. Repositories are then
// read from the installation itself, so the user's OAuth token is not kept.
func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	clearCookie(w, oauthStateCookie, s.secureCookies())

	if !s.validState(r, q.Get("state")) {
		http.Error(w, "invalid or expired state", http.StatusBadRequest)
		return
	}

	installationID := q.Get("installation_id")
	if installationID == "" {
		http.Error(w, "missing installation_id", http.StatusBadRequest)
		return
	}

	// Exchange the code to confirm the user genuinely authorized the app.
	if code := q.Get("code"); code != "" {
		token, err := s.app.ExchangeCode(r.Context(), code)
		if err != nil {
			http.Error(w, "authorization failed", http.StatusBadGateway)
			return
		}
		if login, err := s.app.User(r.Context(), token); err == nil {
			fmt.Printf("connected: installation=%s user=%s\n", installationID, login)
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    s.signer.Sign(installationID),
		Path:     "/",
		MaxAge:   7 * 24 * 3600,
		HttpOnly: true,
		Secure:   s.secureCookies(),
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, "/?connected=1", http.StatusFound)
}

// validState verifies that the state returned by GitHub matches the value we
// signed into the state cookie.
func (s *Server) validState(r *http.Request, returned string) bool {
	if returned == "" {
		return false
	}
	c, err := r.Cookie(oauthStateCookie)
	if err != nil {
		return false
	}
	want, ok := s.signer.Verify(c.Value)
	return ok && want == returned
}

// handleRepos returns, as JSON, the repositories the connected installation
// can access — the data the dashboard renders as the repository picker. It
// requires a valid session cookie carrying the installation_id.
func (s *Server) handleRepos(w http.ResponseWriter, r *http.Request) {
	installationID, ok := s.session(r)
	if !ok {
		http.Error(w, "not connected", http.StatusUnauthorized)
		return
	}

	repos, err := s.app.Repositories(r.Context(), installationID)
	if err != nil {
		fmt.Println("list repos error:", err)
		http.Error(w, "could not list repositories", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(repos); err != nil {
		fmt.Println("encode repos error:", err)
	}
}

// session returns the installation_id from a verified session cookie.
func (s *Server) session(r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", false
	}
	return s.signer.Verify(c.Value)
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

// clearCookie expires a cookie by name.
func clearCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// randomState returns 32 bytes of base64url-encoded randomness.
func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
