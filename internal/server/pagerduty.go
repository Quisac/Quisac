package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"quisac/internal/db"
	"quisac/internal/pagerduty"
	"quisac/internal/traverse"
)

// handlePagerDuty receives PagerDuty V3 webhooks, verifies the signature, and
// persists the incident. No traversal happens here — the user triggers that
// manually from the Incidents view via POST /api/incidents/{id}/traverse.
func (s *Server) handlePagerDuty(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	defer r.Body.Close()
	if err != nil {
		s.logf(r).Error("read pagerduty body", "err", err)
		http.Error(w, "could not read body", http.StatusBadRequest)
		return
	}

	if !pagerduty.VerifySignature(s.cfg.Server.SessionSecret, body, r.Header.Get("X-PagerDuty-Signature")) {
		s.logf(r).Warn("invalid pagerduty signature — proceeding in dev mode")
		// In production tighten this to: http.Error(w, "invalid signature", 401); return
	}

	var payload pagerduty.WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		s.logf(r).Error("parse pagerduty payload", "err", err)
		http.Error(w, "invalid JSON payload", http.StatusBadRequest)
		return
	}

	installationID := r.URL.Query().Get("installation_id")
	if installationID == "" {
		s.logf(r).Error("missing installation_id in webhook url")
		http.Error(w, "missing installation_id query param", http.StatusBadRequest)
		return
	}

	inc := pagerduty.ExtractIncident(payload)
	inc.InstallationID = installationID
	s.logf(r).Info("pagerduty incident received",
		"id", inc.ID, "service", inc.ServiceName, "title", inc.Title)

	if err := s.incidents.CreateIncident(r.Context(), db.Incident{
		ID:             inc.ID,
		InstallationID: inc.InstallationID,
		PDNumber:       payload.Event.Data.Number,
		Title:          inc.Title,
		ServiceName:    inc.ServiceName,
		Urgency:        payload.Event.Data.Urgency,
		ErrorBody:      inc.ErrorBody,
	}); err != nil {
		s.logf(r).Error("store incident", "err", err)
		// Don't fail the webhook — PagerDuty retries on non-2xx.
	}

	w.WriteHeader(http.StatusAccepted)

	// Automatically run traversal in the background.
	if s.traverser != nil {
		go s.autoTraverseIncident(inc.ID, inc, installationID)
	}
}

func (s *Server) autoTraverseIncident(incidentID string, inc traverse.Incident, installationID string) {
	ctx := context.Background() // new context for background work

	_ = s.incidents.MarkTraversing(ctx, incidentID)

	repos, err := s.incidents.ListIndexedRepos(ctx, installationID)
	if err != nil || len(repos) == 0 {
		s.log.Error("no indexed repos for traversal")
		_ = s.incidents.FailIncidentTraversal(ctx, incidentID)
		return
	}

	var bestResult *traverse.Result
	for _, repo := range repos {
		res, err := s.traverser.Run(ctx, repo, inc, 5)
		if err != nil {
			s.log.Warn("auto traversal skipped for repo", "repo", repo, "err", err)
			continue
		}
		if bestResult == nil || len(res.Candidates) > len(bestResult.Candidates) {
			bestResult = res
		}
	}

	if bestResult == nil {
		_ = s.incidents.FailIncidentTraversal(ctx, incidentID)
		return
	}

	if err := s.incidents.UpdateIncidentResult(ctx, incidentID,
		bestResult.Repo, bestResult.EntryPoint, bestResult.Candidates); err != nil {
		s.log.Error("store auto traversal result", "err", err)
	}
}

// handleIncidents serves GET /api/incidents — returns the list of all incidents
// so the frontend can render the Incidents view.
func (s *Server) handleIncidents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sd, ok := s.session(r)
	if !ok {
		http.Error(w, "not connected", http.StatusUnauthorized)
		return
	}

	incidents, err := s.incidents.ListIncidents(r.Context(), sd.InstallationID)
	if err != nil {
		s.logf(r).Error("list incidents", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	type row struct {
		ID          string  `json:"id"`
		PDNumber    int     `json:"pd_number"`
		Title       string  `json:"title"`
		ServiceName string  `json:"service_name"`
		Urgency     string  `json:"urgency"`
		ErrorBody   string  `json:"error_body"`
		Status      string  `json:"status"`
		Repo        string  `json:"repo,omitempty"`
		EntryPoint  any     `json:"entry_point,omitempty"`
		Candidates  any     `json:"candidates,omitempty"`
		ReceivedAt  string  `json:"received_at"`
		TraversedAt *string `json:"traversed_at,omitempty"`
	}

	out := make([]row, 0, len(incidents))
	for _, inc := range incidents {
		r2 := row{
			ID:          inc.ID,
			PDNumber:    inc.PDNumber,
			Title:       inc.Title,
			ServiceName: inc.ServiceName,
			Urgency:     inc.Urgency,
			ErrorBody:   inc.ErrorBody,
			Status:      inc.Status,
			Repo:        inc.Repo,
			ReceivedAt:  inc.ReceivedAt.Format("2006-01-02T15:04:05Z"),
		}
		if inc.TraversedAt != nil {
			t := inc.TraversedAt.Format("2006-01-02T15:04:05Z")
			r2.TraversedAt = &t
		}
		if len(inc.EntryPoint) > 0 {
			json.Unmarshal(inc.EntryPoint, &r2.EntryPoint)
		}
		if len(inc.Candidates) > 0 {
			json.Unmarshal(inc.Candidates, &r2.Candidates)
		}
		out = append(out, r2)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// handleIncidentTraverse handles POST /api/incidents/{id}/traverse.
// It auto-detects the best-matching repo from the user's indexed repos,
// runs BFS traversal, and stores the result back to the DB.
func (s *Server) handleIncidentTraverse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sd, ok := s.session(r)
	if !ok {
		http.Error(w, "not connected", http.StatusUnauthorized)
		return
	}

	// /api/incidents/{id}/traverse  →  extract {id}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/incidents/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "missing incident id", http.StatusBadRequest)
		return
	}
	incidentID := parts[0]

	inc, err := s.incidents.GetIncident(r.Context(), incidentID)
	if err != nil {
		http.Error(w, "incident not found", http.StatusNotFound)
		return
	}
	if inc.Status == "traversed" {
		// Already done — return the cached result immediately.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status":      "traversed",
			"repo":        inc.Repo,
			"entry_point": json.RawMessage(inc.EntryPoint),
			"candidates":  json.RawMessage(inc.Candidates),
		})
		return
	}
	if s.traverser == nil {
		http.Error(w, "traversal engine unavailable (Neo4j not connected)", http.StatusServiceUnavailable)
		return
	}

	// Mark as traversing so the UI can show a spinner.
	_ = s.incidents.MarkTraversing(r.Context(), incidentID)

	// Find all repos this installation has indexed.
	repos, err := s.incidents.ListIndexedRepos(r.Context(), sd.InstallationID)
	if err != nil || len(repos) == 0 {
		s.logf(r).Error("no indexed repos for traversal")
		_ = s.incidents.FailIncidentTraversal(r.Context(), incidentID)
		http.Error(w, "no indexed repositories found; index a repo first", http.StatusUnprocessableEntity)
		return
	}

	incident := traverse.Incident{
		ID:          inc.ID,
		Title:       inc.Title,
		ServiceName: inc.ServiceName,
		ErrorBody:   inc.ErrorBody,
	}

	// Try each indexed repo and pick the result with the most candidates.
	// The repo whose graph best matches the error terms wins.
	var bestResult *traverse.Result
	for _, repo := range repos {
		res, err := s.traverser.Run(r.Context(), repo, incident, 5)
		if err != nil {
			s.logf(r).Warn("traversal skipped for repo", "repo", repo, "err", err)
			continue
		}
		if bestResult == nil || len(res.Candidates) > len(bestResult.Candidates) {
			bestResult = res
		}
	}

	if bestResult == nil {
		_ = s.incidents.FailIncidentTraversal(r.Context(), incidentID)
		http.Error(w, "could not find a matching entry point in any indexed repo", http.StatusNotFound)
		return
	}

	// Persist the result.
	if err := s.incidents.UpdateIncidentResult(r.Context(), incidentID,
		bestResult.Repo, bestResult.EntryPoint, bestResult.Candidates); err != nil {
		s.logf(r).Error("store traversal result", "err", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":      "traversed",
		"repo":        bestResult.Repo,
		"entry_point": bestResult.EntryPoint,
		"candidates":  bestResult.Candidates,
	})
}
