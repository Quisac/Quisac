package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"expvar"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"quisac/internal/config"
	"quisac/internal/db"
	"quisac/internal/github"
	"quisac/internal/pagerduty"
	"quisac/internal/session"
)

const (
	oauthStateCookie = "quisac_oauth_state"
	sessionCookie    = "quisac_session"
)

// SessionStore persists users and their sessions. It is satisfied by *db.Store
// and injected into the server, so handlers can be exercised against a fake.
type SessionStore interface {
	UpsertUser(ctx context.Context, id int64, login string) error
	CreateSession(ctx context.Context, s db.Session) error
	GetSession(ctx context.Context, id string) (db.Session, error)
	DeleteSession(ctx context.Context, id string) error
}

// JobQueue is the durable async-work store the server enqueues into and polls.
// Satisfied by *db.Store.
type JobQueue interface {
	EnqueueJob(ctx context.Context, j db.Job) error
	GetIndexJob(ctx context.Context, id string, userID int64) (db.IndexJobResult, error)
	CountActiveIndexJobs(ctx context.Context, userID int64) (int, error)
}

// Waker nudges the worker pool to pick up freshly enqueued work without waiting
// for its next poll. Satisfied by *worker.Pool.
type Waker interface {
	Wake()
}

// Server holds the dependencies shared across HTTP handlers.
type Server struct {
	cfg    *config.Config
	log    *slog.Logger
	app    *github.App
	signer *session.Signer
	store  SessionStore
	queue  JobQueue
	waker  Waker
	checks []readinessCheck
}

// New builds a Server from a resolved config and its dependencies.
func New(cfg *config.Config, log *slog.Logger, store SessionStore, queue JobQueue, waker Waker, app *github.App) *Server {
	return &Server{
		cfg:    cfg,
		log:    log,
		app:    app,
		signer: session.NewSigner(cfg.Server.SessionSecret),
		store:  store,
		queue:  queue,
		waker:  waker,
	}
}

// Handler returns the router with all routes registered, wrapped in the
// request-id and access-log middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/pagerduty/webhook", pagerduty.IncidentHandler)
	mux.HandleFunc("/api/index", s.handleIndex)
	mux.HandleFunc("/api/graph/", s.handleGraph)
	mux.HandleFunc("/webhook", s.handleWebhook)
	mux.HandleFunc("/api/connect", s.handleConnect)
	mux.HandleFunc("/api/install", s.handleInstall)
	mux.HandleFunc("/github/callback", s.handleCallback)
	mux.HandleFunc("/api/me", s.handleMe)
	mux.HandleFunc("/api/logout", s.handleLogout)
	mux.HandleFunc("/api/repos", s.handleRepos)
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	mux.Handle("/debug/vars", expvar.Handler())
	mux.HandleFunc("/", s.handleStatic)
	return s.withRequestID(s.withLogging(mux))
}

// handleConnect starts the connect flow via OAuth authorize.
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	s.startFlow(w, r, s.app.AuthorizeURL)
}

// handleInstall sends the user to GitHub's installation screen.
func (s *Server) handleInstall(w http.ResponseWriter, r *http.Request) {
	s.startFlow(w, r, s.app.InstallURL)
}

// startFlow mints a CSRF state, stores it in a signed short-lived cookie, and
// redirects to the URL built by urlFor(state).
func (s *Server) startFlow(w http.ResponseWriter, r *http.Request, urlFor func(string) string) {
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
		Secure:   s.secureCookies(r),
		SameSite: http.SameSiteLaxMode,
	})
	w.Header().Set("Location", urlFor(state))
	w.WriteHeader(http.StatusFound)
}

// handleCallback completes the install + authorize flow.
func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	clearCookie(w, oauthStateCookie, s.secureCookies(r))

	if !s.validState(r, q.Get("state")) {
		http.Error(w, "invalid or expired state", http.StatusBadRequest)
		return
	}

	code := q.Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	token, err := s.app.ExchangeCode(r.Context(), code)
	if err != nil {
		s.logf(r).Error("oauth: exchange code", "err", err)
		http.Error(w, "authorization failed", http.StatusBadGateway)
		return
	}
	if token == "" {
		http.Error(w, "authorization failed: no user token", http.StatusBadGateway)
		return
	}
	userID, login, err := s.app.User(r.Context(), token)
	if err != nil {
		s.logf(r).Error("oauth: read user", "err", err)
		http.Error(w, "could not read user", http.StatusBadGateway)
		return
	}

	installs, err := s.app.UserInstallations(r.Context(), token)
	if err != nil {
		s.logf(r).Error("oauth: read installations", "err", err)
		http.Error(w, "could not read installations", http.StatusBadGateway)
		return
	}

	installationID := q.Get("installation_id")
	if installationID == "" {
		if len(installs) == 0 {
			s.startFlow(w, r, s.app.InstallURL)
			return
		}
		installationID = strconv.FormatInt(installs[0].ID, 10)
	}

	s.logf(r).Info("connected", "installation", installationID, "user", login)

	if err := s.startSession(r.Context(), w, r, userID, login, installationID); err != nil {
		s.logf(r).Error("start session", "err", err)
		http.Error(w, "could not create session", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusFound)
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

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	sd, ok := s.session(r)
	if !ok {
		http.Error(w, "not connected", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"connected":      true,
		"login":          sd.Login,
		"installationId": sd.InstallationID,
	})
}

// handleLogout deletes the persisted session and clears the cookie.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.store.DeleteSession(r.Context(), c.Value); err != nil {
			s.logf(r).Error("delete session", "err", err)
		}
	}
	clearCookie(w, sessionCookie, s.secureCookies(r))
	w.WriteHeader(http.StatusNoContent)
}

// handleRepos returns the repositories the connected installation can access.
func (s *Server) handleRepos(w http.ResponseWriter, r *http.Request) {
	sd, ok := s.session(r)
	if !ok {
		http.Error(w, "not connected", http.StatusUnauthorized)
		return
	}

	repos, err := s.app.Repositories(r.Context(), sd.InstallationID)
	if err != nil {
		s.logf(r).Error("list repos", "err", err)
		http.Error(w, "could not list repositories", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(repos); err != nil {
		s.logf(r).Error("encode repos", "err", err)
	}
}

// sessionData is the session payload handlers consume.
type sessionData struct {
	UserID         int64
	InstallationID string
	Login          string
}

// sessionTTL is how long a persisted session stays valid.
const sessionTTL = 7 * 24 * time.Hour

// startSession upserts the user, persists a new session, and sets the cookie to
// the opaque session token. The token is high-entropy, so the DB lookup is the
// only check needed — no signing.
func (s *Server) startSession(ctx context.Context, w http.ResponseWriter, r *http.Request, userID int64, login, installationID string) error {
	if err := s.store.UpsertUser(ctx, userID, login); err != nil {
		return err
	}
	token, err := randomState()
	if err != nil {
		return err
	}
	if err := s.store.CreateSession(ctx, db.Session{
		ID:             token,
		UserID:         userID,
		InstallationID: installationID,
		ExpiresAt:      time.Now().Add(sessionTTL),
	}); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.secureCookies(r),
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// session looks up the request's session token in the store. It returns false
// for a missing, unknown, or expired session.
func (s *Server) session(r *http.Request) (sessionData, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return sessionData{}, false
	}
	sess, err := s.store.GetSession(r.Context(), c.Value)
	if err != nil {
		return sessionData{}, false
	}
	return sessionData{UserID: sess.UserID, InstallationID: sess.InstallationID, Login: sess.Login}, true
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

// secureCookies reports whether cookies should carry the Secure flag.
// It reads the X-Forwarded-Proto header set by reverse proxies (Railway, ngrok,
// nginx, etc.) when they terminate TLS and forward as plain HTTP. This is the
// standard way to detect HTTPS behind a proxy without hardcoding env vars.
func (s *Server) secureCookies(r *http.Request) bool {
	return r.Header.Get("X-Forwarded-Proto") == "https"
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
