package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"quisac/internal/db"
)

// maxActiveIndexJobs caps how many full-index jobs a single user may have
// pending or running at once — a simple per-user rate limit that stops one user
// from queuing unbounded expensive work.
const maxActiveIndexJobs = 3

// handleIndex enqueues a full index of a repo and returns a job id (202). The
// work is picked up by the worker pool; the browser polls /api/graph/{id}.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sd, ok := s.session(r)
	if !ok {
		http.Error(w, "not connected", http.StatusUnauthorized)
		return
	}

	var body struct {
		Repo string `json:"repo"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.Count(body.Repo, "/") != 1 {
		http.Error(w, `body must be {"repo":"owner/repo"}`, http.StatusBadRequest)
		return
	}

	// Rate limit: reject when the user already has too much in-flight work.
	active, err := s.queue.CountActiveIndexJobs(r.Context(), sd.UserID)
	if err != nil {
		s.logf(r).Error("count active index jobs", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if active >= maxActiveIndexJobs {
		http.Error(w, "too many in-flight index jobs; try again shortly", http.StatusTooManyRequests)
		return
	}

	jobID, err := randomState()
	if err != nil {
		http.Error(w, "could not create job", http.StatusInternalServerError)
		return
	}
	jobID = jobID[:16]

	if err := s.queue.EnqueueJob(r.Context(), db.Job{
		ID:             jobID,
		Kind:           db.JobFullIndex,
		Repo:           body.Repo,
		InstallationID: sd.InstallationID,
		UserID:         sd.UserID,
	}); err != nil {
		s.logf(r).Error("enqueue index job", "err", err, "repo", body.Repo)
		http.Error(w, "could not enqueue job", http.StatusInternalServerError)
		return
	}
	s.waker.Wake()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"jobId": jobID})
}

// handleGraph reports an index job's status and, when done, its graph. The job
// is scoped to the requesting user, so one user cannot read another's result.
func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sd, ok := s.session(r)
	if !ok {
		http.Error(w, "not connected", http.StatusUnauthorized)
		return
	}

	jobID := strings.TrimPrefix(r.URL.Path, "/api/graph/")
	res, err := s.queue.GetIndexJob(r.Context(), jobID, sd.UserID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.Error(w, "job not found", http.StatusNotFound)
			return
		}
		s.logf(r).Error("get index job", "err", err, "job", jobID)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	switch res.Status {
	case db.StatusFailed:
		// The detailed error is logged by the worker; the client sees a generic
		// message so internal paths and driver detail never leak.
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"status": "failed", "error": "indexing failed"})
	case db.StatusDone:
		fmt.Fprintf(w, `{"status":"done","graph":%s}`, res.Graph)
	default:
		json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
	}
}
