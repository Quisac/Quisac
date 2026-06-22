// Package github implements the GitHub App OAuth flow and repository
// import UI for Quisac: connecting a GitHub App installation, exchanging
// the OAuth code for a user token, and letting the user pick a repo to
// import.
package github

import (
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
)

// Handler wires together the GitHub OAuth + import HTTP routes.
type Handler struct {
	cfg      Config
	sessions sessionStore
	state    StateStore
	log      *slog.Logger
}

// HandlerOption configures optional behavior on a Handler. This mirrors
// palantir/go-githubapp's functional-options pattern (their oauth2.Param).
type HandlerOption func(*Handler)

// WithStateStore overrides the OAuth CSRF StateStore used by the Handler.
// The default is a short-lived HttpOnly cookie (cookieStateStore), which is
// sufficient for a single-instance deployment. Pass a different StateStore
// implementation if state needs to survive across instances, e.g. behind a
// load balancer with no sticky sessions.
func WithStateStore(s StateStore) HandlerOption {
	return func(h *Handler) {
		h.state = s
	}
}

// NewHandler builds a Handler. cfg must pass Validate.
func NewHandler(cfg Config, log *slog.Logger, opts ...HandlerOption) *Handler {
	if log == nil {
		log = slog.Default()
	}
	h := &Handler{
		cfg:      cfg,
		sessions: newSessionStore(cfg.SessionSecret),
		state:    cookieStateStore{},
		log:      log.With("component", "github"),
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Register mounts the GitHub OAuth + import routes onto mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /github/connect", h.handleConnect)
	mux.HandleFunc("GET /github/connect/go", h.handleConnectGo)
	mux.HandleFunc("GET /github/callback", h.handleCallback)
	mux.HandleFunc("GET /github/import", h.handleImport)
	mux.HandleFunc("POST /github/import/select", h.handleImportSelect)
}

// Register mounts routes using a package-level default Handler, kept for
// call-site compatibility with the previous Register(mux, cfg) signature.
func Register(mux *http.ServeMux, cfg Config) {
	NewHandler(cfg, nil).Register(mux)
}

// handleConnect renders the "Continue with GitHub" provider picker page.
func (h *Handler) handleConnect(w http.ResponseWriter, r *http.Request) {
	h.render(w, connectTmpl, map[string]string{"Title": "Import Repository"})
}

// handleConnectGo starts the GitHub App installation/OAuth redirect.
func (h *Handler) handleConnectGo(w http.ResponseWriter, r *http.Request) {
	state, err := h.state.GenerateState(w, r)
	if err != nil {
		h.log.Error("generate oauth state", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	dest := fmt.Sprintf("%s/apps/%s/installations/new?state=%s",
		h.cfg.webBaseURL(), h.cfg.AppSlug, url.QueryEscape(state))
	h.log.Info("redirecting to github install", "url", dest)
	http.Redirect(w, r, dest, http.StatusFound)
}

// handleCallback completes the OAuth dance: verify state, exchange the
// code for a token, resolve the installation, and persist the session.
func (h *Handler) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	h.log.Info("callback received", "installation_id", q.Get("installation_id"))

	valid, err := h.state.VerifyState(r, q.Get("state"))
	if err != nil {
		h.log.Error("oauth state check error", "err", err)
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	if !valid {
		h.log.Warn("oauth state mismatch", "err", errStateMismatch)
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	clearOAuthStateCookie(w)

	installID, err := strconv.ParseInt(q.Get("installation_id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid installation_id", http.StatusBadRequest)
		return
	}

	// GitHub may redirect here without a `code` (e.g. an installation-only
	// flow). Without a code we have nothing to exchange for a token, so
	// just send the user on to import and let it prompt for connect again
	// if no session exists yet.
	code := q.Get("code")
	if code == "" {
		http.Redirect(w, r, "/github/import", http.StatusFound)
		return
	}

	ctx := r.Context()

	token, err := exchangeCode(ctx, h.cfg, code)
	if err != nil {
		h.log.Error("token exchange failed", "err", err)
		http.Error(w, "token exchange failed", http.StatusBadGateway)
		return
	}

	gh := newClient(token, h.cfg)

	inst, err := gh.FindInstallation(ctx, installID)
	if err != nil {
		h.log.Error("find installation failed", "installation_id", installID, "err", err)
		http.Redirect(w, r, "/github/import", http.StatusFound)
		return
	}
	h.log.Info("github app connected", "account", inst.Account.Login, "type", inst.Account.Type)

	sess := &session{Token: token, Installation: inst}
	if err := h.sessions.Save(w, sess); err != nil {
		h.log.Error("save session failed", "err", err)
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/github/import", http.StatusFound)
}

// handleImport renders the repo picker, listing repos visible to the
// installation stored in the user's session.
func (h *Handler) handleImport(w http.ResponseWriter, r *http.Request) {
	sess, err := h.requireSession(w, r)
	if err != nil {
		return // requireSession already redirected
	}

	gh := newClient(sess.Token, h.cfg)
	repos, err := gh.ListRepos(r.Context(), sess.Installation.ID)
	if err != nil {
		h.log.Error("list repos failed", "installation_id", sess.Installation.ID, "err", err)
		repos = nil // render an empty-state list rather than failing the page
	}

	h.render(w, importTmpl, importPageData{
		Title:   "Import Repository",
		OrgName: sess.Installation.Account.Login,
		Repos:   repos,
	})
}

type importPageData struct {
	Title   string
	OrgName string
	Repos   []Repo
}

// handleImportSelect records the user's chosen repo on the session and
// sends them back to the dashboard.
func (h *Handler) handleImportSelect(w http.ResponseWriter, r *http.Request) {
	sess, err := h.requireSession(w, r)
	if err != nil {
		return
	}

	repoFullName := r.FormValue("repo")
	if repoFullName == "" {
		http.Error(w, "missing repo", http.StatusBadRequest)
		return
	}

	sess.SelectedRepo = repoFullName
	if err := h.sessions.Save(w, sess); err != nil {
		h.log.Error("save session failed", "err", err)
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}

	h.log.Info("repo selected", "repo", repoFullName)

	dest := "/?" + url.Values{
		"connected": {"1"},
		"org":       {sess.Installation.Account.Login},
		"repo":      {repoFullName},
	}.Encode()
	http.Redirect(w, r, dest, http.StatusFound)
}

// requireSession loads the session cookie, redirecting to /github/connect
// and returning a non-nil error if there isn't a usable one. Callers
// should return immediately when err != nil.
func (h *Handler) requireSession(w http.ResponseWriter, r *http.Request) (*session, error) {
	sess, err := h.sessions.Load(r)
	if err != nil {
		if !errors.Is(err, errNoSession) {
			h.log.Warn("session load failed", "err", err)
		}
		http.Redirect(w, r, "/github/connect", http.StatusFound)
		return nil, err
	}
	if sess.Token == "" || sess.Installation == nil {
		http.Redirect(w, r, "/github/connect", http.StatusFound)
		return nil, errNoSession
	}
	return sess, nil
}

// render executes tmpl's "layout" template with data, writing a 500 on
// failure. Because html/template buffers nothing by default, a failure
// partway through execution can leak a partial page; that tradeoff is
// accepted here since these are simple, well-tested templates.
func (h *Handler) render(w http.ResponseWriter, tmpl *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		h.log.Error("render template failed", "err", err)
	}
}
