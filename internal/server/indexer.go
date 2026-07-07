package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type jobStatus string

const (
	jobPending jobStatus = "pending"
	jobDone    jobStatus = "done"
	jobFailed  jobStatus = "failed"
)

type indexJob struct {
	Status    jobStatus
	GraphJSON string
	Error     string
	StartedAt time.Time
}

// jobStore tracks async index jobs so the browser can poll for completion.
type jobStore struct {
	mu   sync.RWMutex
	jobs map[string]*indexJob
}

func newJobStore() *jobStore { return &jobStore{jobs: map[string]*indexJob{}} }

func (js *jobStore) set(id string, j *indexJob) {
	js.mu.Lock()
	js.jobs[id] = j
	js.mu.Unlock()
}

func (js *jobStore) update(id string, fn func(*indexJob)) {
	js.mu.Lock()
	if j, ok := js.jobs[id]; ok {
		fn(j)
	}
	js.mu.Unlock()
}

func (js *jobStore) get(id string) (indexJob, bool) {
	js.mu.RLock()
	defer js.mu.RUnlock()
	j, ok := js.jobs[id]
	if !ok {
		return indexJob{}, false
	}
	return *j, true
}

// handleIndex enqueues a full index of a repo and returns a job id (202). The
// work runs in the background so the browser never waits on a long operation.
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

	jobID, err := randomState()
	if err != nil {
		http.Error(w, "could not create job", http.StatusInternalServerError)
		return
	}
	jobID = jobID[:16]
	s.jobs.set(jobID, &indexJob{Status: jobPending, StartedAt: time.Now()})

	installID, repo := sd.InstallationID, body.Repo
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()

		g, err := s.indexer.FullIndex(ctx, installID, repo, "")
		if err != nil {
			fmt.Printf("index job %s: %v\n", jobID, err)
			s.jobs.update(jobID, func(j *indexJob) { j.Status, j.Error = jobFailed, err.Error() })
			return
		}
		gj, err := g.ToJSON()
		if err != nil {
			s.jobs.update(jobID, func(j *indexJob) { j.Status, j.Error = jobFailed, err.Error() })
			return
		}
		s.jobs.update(jobID, func(j *indexJob) { j.Status, j.GraphJSON = jobDone, gj })
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"jobId": jobID})
}

// handleGraph reports an index job's status and, when done, its graph.
func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := s.session(r); !ok {
		http.Error(w, "not connected", http.StatusUnauthorized)
		return
	}

	jobID := strings.TrimPrefix(r.URL.Path, "/api/graph/")
	job, ok := s.jobs.get(jobID)
	if !ok {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	switch job.Status {
	case jobFailed:
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"status": "failed", "error": job.Error})
	case jobDone:
		fmt.Fprintf(w, `{"status":"done","graph":%s}`, job.GraphJSON)
	default:
		json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
	}
}
