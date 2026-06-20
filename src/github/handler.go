package github

import (
	"log"
	"net/http"
	"net/url"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{
		service: service,
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/github/connect", h.Connect)
	mux.HandleFunc("/github/callback", h.Callback)
}

func (h *Handler) Connect(
	w http.ResponseWriter,
	r *http.Request,
) {
	state, err := RandomState()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	SetStateCookie(w, state)

	http.Redirect(
		w,
		r,
		h.service.InstallURL(state),
		http.StatusFound,
	)
}

func (h *Handler) Callback(
	w http.ResponseWriter,
	r *http.Request,
) {
	cookie, err := r.Cookie(StateCookieName)
	if err != nil {
		http.Error(w, "missing state", http.StatusBadRequest)
		return
	}

	state := r.URL.Query().Get("state")

	if cookie.Value != state {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}

	ClearStateCookie(w)

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

	token, err := h.service.ExchangeCode(code)
	if err != nil {
		log.Println(err)
		http.Error(w, "oauth failed", http.StatusBadGateway)
		return
	}

	inst, err := h.service.FindInstallation(
		token,
		installationID,
	)
	if err != nil {
		http.Redirect(w, r, "/?connected=1", http.StatusFound)
		return
	}

	if err := SaveSession(w, token, inst); err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}

	redirect := "/?connected=1&org=" +
		url.QueryEscape(inst.Account.Login)

	http.Redirect(
		w,
		r,
		redirect,
		http.StatusFound,
	)
}
