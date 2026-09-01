package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"quisac/internal/core"
	"quisac/internal/db"
	"quisac/internal/pagerduty"
	"quisac/internal/traverse"
	"quisac/internal/worker"
)

// handlePagerDuty receives PagerDuty V3 webhooks, verifies the signature,
// persists the incident, and queues it for resolution.
//
// Resolution is a durable job rather than a goroutine: it spends model tokens
// and can open pull requests, so it must survive the process restarting.
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

	// An accepted delivery spends model tokens and can open a pull request, so a
	// forged one is not a nuisance. VerifySignature passes everything when no
	// secret is configured, which is the documented local-dev path only.
	secret := s.cfg.Server.PagerDutyWebhookSecret
	if secret == "" {
		s.logf(r).Warn("PAGERDUTY_WEBHOOK_SECRET unset — accepting deliveries unverified")
	} else if !pagerduty.VerifySignature(secret, body, r.Header.Get("X-PagerDuty-Signature")) {
		s.logf(r).Warn("rejected pagerduty delivery: invalid signature")
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
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
		// Fail the delivery so PagerDuty retries: an incident we did not store is
		// an incident we will never resolve.
		s.logf(r).Error("store incident", "err", err)
		http.Error(w, "could not store incident", http.StatusInternalServerError)
		return
	}

	if err := s.enqueueResolve(r.Context(), inc.ID, installationID); err != nil {
		s.logf(r).Error("enqueue resolution", "err", err, "incident", inc.ID)
		http.Error(w, "could not queue resolution", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

// enqueueResolve queues the fix pipeline for an incident. The job is filed under
// a per-incident key because the repository is not known until traversal runs,
// and an alert must not wait behind a long index of the repo it will analyse.
func (s *Server) enqueueResolve(ctx context.Context, incidentID, installationID string) error {
	payload, err := json.Marshal(worker.ResolvePayload{IncidentID: incidentID})
	if err != nil {
		return err
	}
	jobID, err := randomState()
	if err != nil {
		return err
	}

	if err := s.queue.EnqueueJob(ctx, db.Job{
		ID:             jobID[:16],
		Kind:           db.JobResolve,
		Repo:           db.ResolveRepoKey(incidentID),
		InstallationID: installationID,
		Payload:        payload,
		// Resolution costs tokens and can open a PR, so give up early.
		MaxAttempts: 2,
	}); err != nil {
		return err
	}
	s.waker.Wake()
	return nil
}

// incidentRow is the JSON shape the Incidents view renders.
type incidentRow struct {
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

	Resolution resolutionRow `json:"resolution"`
}

// resolutionRow is the fix pipeline's outcome, as the UI consumes it.
type resolutionRow struct {
	Status     string  `json:"status"`
	Method     string  `json:"method,omitempty"`
	RootCause  string  `json:"root_cause,omitempty"`
	Confidence float64 `json:"confidence"`
	Summary    string  `json:"summary,omitempty"`
	Files      any     `json:"files,omitempty"`
	PRURL      string  `json:"pr_url,omitempty"`
	PRNumber   int     `json:"pr_number,omitempty"`
	Branch     string  `json:"branch,omitempty"`
	// Tokens is zero when the incident was resolved from structure and cache alone.
	Tokens     int     `json:"tokens"`
	Error      string  `json:"error,omitempty"`
	ResolvedAt *string `json:"resolved_at,omitempty"`
}

func toIncidentRow(inc db.Incident) incidentRow {
	row := incidentRow{
		ID:          inc.ID,
		PDNumber:    inc.PDNumber,
		Title:       inc.Title,
		ServiceName: inc.ServiceName,
		Urgency:     inc.Urgency,
		ErrorBody:   inc.ErrorBody,
		Status:      inc.Status,
		Repo:        inc.Repo,
		ReceivedAt:  inc.ReceivedAt.Format("2006-01-02T15:04:05Z"),
		Resolution: resolutionRow{
			Status:     inc.Resolution.Status,
			Method:     inc.Resolution.Method,
			RootCause:  inc.Resolution.RootCause,
			Confidence: float64(inc.Resolution.Confidence),
			Summary:    inc.Resolution.Summary,
			PRURL:      inc.Resolution.PRURL,
			PRNumber:   inc.Resolution.PRNumber,
			Branch:     inc.Resolution.Branch,
			Tokens:     inc.Resolution.InputTokens + inc.Resolution.OutputTokens,
			Error:      inc.Resolution.Error,
		},
	}
	if inc.TraversedAt != nil {
		t := inc.TraversedAt.Format("2006-01-02T15:04:05Z")
		row.TraversedAt = &t
	}
	if inc.Resolution.ResolvedAt != nil {
		t := inc.Resolution.ResolvedAt.Format("2006-01-02T15:04:05Z")
		row.Resolution.ResolvedAt = &t
	}
	// Already JSON; decode so they nest rather than arriving escaped.
	if len(inc.EntryPoint) > 0 {
		json.Unmarshal(inc.EntryPoint, &row.EntryPoint)
	}
	if len(inc.Candidates) > 0 {
		json.Unmarshal(inc.Candidates, &row.Candidates)
	}
	if len(inc.Resolution.Files) > 0 {
		json.Unmarshal(inc.Resolution.Files, &row.Resolution.Files)
	}
	return row
}

// handleIncidents serves GET /api/incidents — the list the Incidents view polls.
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

	out := make([]incidentRow, 0, len(incidents))
	for _, inc := range incidents {
		out = append(out, toIncidentRow(inc))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// handleIncidentRoute dispatches the per-incident subroutes:
//
//	POST   /api/incidents/{id}/resolve  — re-run the fix pipeline
//	GET    /api/incidents/{id}/graph    — the fault subgraph behind the traversal
//	DELETE /api/incidents/{id}          — the engineer closed it out; drop the row
//
// /traverse is accepted as an alias for /resolve: traversal is no longer a stage
// a caller can run on its own, it is the middle tier of the same pipeline.
func (s *Server) handleIncidentRoute(w http.ResponseWriter, r *http.Request) {
	sd, ok := s.session(r)
	if !ok {
		http.Error(w, "not connected", http.StatusUnauthorized)
		return
	}

	// /api/incidents/{id}/{action}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/incidents/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "missing incident id", http.StatusBadRequest)
		return
	}
	incidentID := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	inc, err := s.incidents.GetIncident(r.Context(), incidentID)
	if err != nil {
		http.Error(w, "incident not found", http.StatusNotFound)
		return
	}
	// An incident belongs to the installation that received it; a session for a
	// different installation must not be able to read it or trigger work against
	// it.
	if inc.InstallationID != sd.InstallationID {
		http.Error(w, "incident not found", http.StatusNotFound)
		return
	}

	if action == "graph" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.serveFaultGraph(w, r, inc)
		return
	}

	// Marking an incident resolved deletes it. The learned fix survives in
	// incident_patterns, which is keyed by fingerprint rather than by incident,
	// so clearing the feed costs the pattern cache nothing.
	if r.Method == http.MethodDelete && action == "" {
		if err := s.incidents.DeleteIncident(r.Context(), incidentID, sd.InstallationID); err != nil {
			if errors.Is(err, db.ErrNotFound) {
				http.Error(w, "incident not found", http.StatusNotFound)
				return
			}
			s.logf(r).Error("delete incident", "err", err, "incident", incidentID)
			http.Error(w, "could not delete incident", http.StatusInternalServerError)
			return
		}
		s.logf(r).Info("incident resolved and deleted", "incident", incidentID)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if inc.Resolution.Status == "resolving" {
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]any{"status": "resolving"})
		return
	}

	if err := s.enqueueResolve(r.Context(), incidentID, sd.InstallationID); err != nil {
		s.logf(r).Error("enqueue resolution", "err", err, "incident", incidentID)
		http.Error(w, "could not queue resolution", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]any{"status": "resolving"})
}

// serveFaultGraph answers GET /api/incidents/{id}/graph with the subgraph that
// connects the incident's ranked candidates back to its entry point.
//
// The ranking itself is read from the incident row — it is the result that was
// stored when the traversal ran, not a fresh walk. Only the paths are queried
// live, so what is drawn is the graph as it exists now, annotated with the
// scores as they were then.
func (s *Server) serveFaultGraph(w http.ResponseWriter, r *http.Request, inc db.Incident) {
	if s.faults == nil {
		http.Error(w, "graph queries unavailable", http.StatusServiceUnavailable)
		return
	}

	var entry core.Node
	if len(inc.EntryPoint) > 0 {
		json.Unmarshal(inc.EntryPoint, &entry)
	}
	var candidates []traverse.CandidateNode
	if len(inc.Candidates) > 0 {
		json.Unmarshal(inc.Candidates, &candidates)
	}

	// No traversal has landed yet (a pattern-cache hit never runs one), so there
	// is nothing to draw. That is an empty answer, not an error.
	if inc.Repo == "" || entry.ID == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(traverse.FaultGraph{
			Repo:  inc.Repo,
			Nodes: []traverse.FaultNode{},
			Edges: []core.Edge{},
		})
		return
	}

	fg, err := s.faults.FaultGraph(r.Context(), traverse.FaultQuery{
		Repo:          inc.Repo,
		EntryPointIDs: []string{entry.ID},
		Candidates:    candidates,
		MaxDepth:      int(s.cfg.Resolution.MaxDepth),
	})
	if err != nil {
		s.logf(r).Error("fault graph", "err", err, "incident", inc.ID, "repo", inc.Repo)
		http.Error(w, "could not read fault graph", http.StatusInternalServerError)
		return
	}
	if fg.Nodes == nil {
		fg.Nodes = []traverse.FaultNode{}
	}
	if fg.Edges == nil {
		fg.Edges = []core.Edge{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(fg)
}
