package server

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type DatadogPayload struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Priority  string   `json:"priority"`
	AlertType string   `json:"alert_type"`
	Host      string   `json:"host"`
	Tags      []string `json:"tags"`
	Date      int64    `json:"date"`
}

func (s *Server) handleDatadogWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	incomingSecret := r.Header.Get("X-Quisac-Secret")
	if incomingSecret == "" {
		http.Error(w, "missing secret", http.StatusUnauthorized)
		return
	}
	installationID, ok := s.ddStore.InstallationBySecret(incomingSecret)
	if !ok {
		http.Error(w, "unknown secret", http.StatusUnauthorized)
		return
	}
	var payload DatadogPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	fmt.Printf("datadog alert: installation=%s title=%q priority=%s\n",
		installationID, payload.Title, payload.Priority)
	// TODO: go s.pipeline.Run(installationID, payload)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleDatadogProvision(w http.ResponseWriter, r *http.Request) {
	sd, ok := s.session(r)
	if !ok {
		http.Error(w, "not connected", http.StatusUnauthorized)
		return
	}
	secret, err := s.ddStore.Provision(sd.InstallationID)
	if err != nil {
		http.Error(w, "could not provision secret", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"webhook_url": "https://quisac.com/datadog/webhook",
		"secret":      secret,
	})
}

func (s *Server) handleDatadogRotate(w http.ResponseWriter, r *http.Request) {
	sd, ok := s.session(r)
	if !ok {
		http.Error(w, "not connected", http.StatusUnauthorized)
		return
	}
	secret, err := s.ddStore.Rotate(sd.InstallationID)
	if err != nil {
		http.Error(w, "could not rotate secret", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"webhook_url": "https://quisac.com/datadog/webhook",
		"secret":      secret,
	})
}
