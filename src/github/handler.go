package github

import (
	"log"
	"net/http"
	"net/url"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/github/connect", h.connect)
	mux.HandleFunc("/github/callback", h.callback)
}

func (h *Handler) connect(w http.ResponseWriter, r *http.Request) {
	state, err := randomState()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	setStateCookie(w, state)

	http.Redirect(
		w,
		r,
		h.svc.InstallURL(state),
		http.StatusFound,
	)
}

func (h *Handler) callback(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(stateCookie)
	if err != nil || cookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}

	clearStateCookie(w)

	installationID := r.URL.Query().Get("installation_id")
	if installationID == "" {
		http.Error(w, "missing installation_id", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Redirect(w, r, "/?connected=1", http.StatusFound)
		return
	}

	token, err := h.svc.ExchangeCode(code)
	if err != nil {
		log.Println("exchange code:", err)
		http.Error(w, "oauth failed", http.StatusBadGateway)
		return
	}

	inst, err := h.svc.FindInstallation(token, installationID)
	if err != nil {
		http.Redirect(w, r, "/?connected=1", http.StatusFound)
		return
	}

	if err := saveSession(w, token, inst); err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}

	redirect := "/?connected=1&org=" +
		url.QueryEscape(inst.Account.Login)

	http.Redirect(w, r, redirect, http.StatusFound)
}
