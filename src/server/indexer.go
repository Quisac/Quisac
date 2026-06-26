package server

// Add these two routes inside Handler() in server.go, alongside the existing ones:
//
//   mux.HandleFunc("/api/index", s.handleIndex)
//   mux.HandleFunc("/api/graph/", s.handleGraph)  // note trailing slash — catches /api/graph/<id>

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"quisac/src/filewalk"
)

// ── Job store ────────────────────────────────────────────────────────────────

type jobStatus string

const (
	jobPending jobStatus = "pending"
	jobDone    jobStatus = "done"
	jobFailed  jobStatus = "failed"
)

type indexJob struct {
	Status    jobStatus `json:"status"`
	GraphJSON string    `json:"graph,omitempty"`
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"-"`
}

var (
	jobsMu sync.RWMutex
	jobs   = map[string]*indexJob{}
)

func setJob(id string, j *indexJob) {
	jobsMu.Lock()
	jobs[id] = j
	jobsMu.Unlock()
}

func getJob(id string) (*indexJob, bool) {
	jobsMu.RLock()
	j, ok := jobs[id]
	jobsMu.RUnlock()
	return j, ok
}

// ── POST /api/index ──────────────────────────────────────────────────────────
//
// Body: { "repo": "owner/repo" }
// Response: { "jobId": "<id>" }   (202 Accepted)
//
// The handler validates the session, enqueues a background job that downloads
// the repo and runs filewalk.ReadRepo, then returns immediately so the browser
// is never left waiting for a potentially long operation.

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
		Repo string `json:"repo"` // "owner/repo"
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Repo == "" {
		http.Error(w, "body must be {\"repo\":\"owner/repo\"}", http.StatusBadRequest)
		return
	}
	// Basic sanity-check: must look like "owner/repo".
	if strings.Count(body.Repo, "/") != 1 {
		http.Error(w, "repo must be in owner/repo form", http.StatusBadRequest)
		return
	}

	// Generate a short job ID from randomState() (already in server.go).
	jobID, err := randomState()
	if err != nil {
		http.Error(w, "could not create job", http.StatusInternalServerError)
		return
	}
	// Trim to 16 chars — plenty of uniqueness, easier to log.
	if len(jobID) > 16 {
		jobID = jobID[:16]
	}

	job := &indexJob{Status: jobPending, StartedAt: time.Now()}
	setJob(jobID, job)

	// Capture what we need; do not capture r or w — they're gone after this
	// handler returns.
	installID := sd.InstallationID
	repoName := body.Repo
	app := s.app

	go func() {
		// Never use r.Context() in background jobs.
		ctx, cancel := context.WithTimeout(
			context.Background(),
			30*time.Minute,
		)
		defer cancel()

		fmt.Printf("index job %s: downloading %s\n", jobID, repoName)

		dir, cleanup, err := app.DownloadRepo(ctx, installID, repoName)
		if err != nil {
			fmt.Printf("index job %s: download failed: %v\n", jobID, err)

			jobsMu.Lock()
			job.Status = jobFailed
			job.Error = err.Error()
			jobsMu.Unlock()

			return
		}
		defer cleanup()

		fmt.Printf("index job %s: running filewalk on %s\n", jobID, dir)

		graph := filewalk.ReadRepo(dir)

		gj, err := graph.ToJSON()
		if err != nil {
			fmt.Printf("index job %s: ToJSON failed: %v\n", jobID, err)

			jobsMu.Lock()
			job.Status = jobFailed
			job.Error = err.Error()
			jobsMu.Unlock()

			return
		}

		elapsed := time.Since(job.StartedAt).Round(time.Millisecond)

		fmt.Printf("index job %s: done in %s\n", jobID, elapsed)

		jobsMu.Lock()
		job.Status = jobDone
		job.GraphJSON = gj
		jobsMu.Unlock()
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"jobId": jobID})
}

// ── GET /api/graph/<jobId> ───────────────────────────────────────────────────
//
// While the job is running:   { "status": "pending" }
// On failure:                 { "status": "failed",  "error": "..." }
// On success:                 { "status": "done",    "graph": { ...full graph object... } }
//
// The graph value is inlined as a real JSON object (not a string) so the
// frontend can use it directly without a second JSON.parse().

func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := s.session(r); !ok {
		http.Error(w, "not connected", http.StatusUnauthorized)
		return
	}

	// Path is /api/graph/<jobId>
	jobID := strings.TrimPrefix(r.URL.Path, "/api/graph/")
	if jobID == "" {
		http.Error(w, "missing job id", http.StatusBadRequest)
		return
	}

	job, ok := getJob(jobID)
	if !ok {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	jobsMu.RLock()
	status := job.Status
	errMsg := job.Error
	graphJSON := job.GraphJSON
	jobsMu.RUnlock()

	switch status {
	case jobPending:
		json.NewEncoder(w).Encode(map[string]string{"status": "pending"})

	case jobFailed:
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"status": "failed", "error": errMsg})

	case jobDone:
		// Write the envelope manually so graph is an object, not a JSON string.
		fmt.Fprintf(w, `{"status":"done","graph":%s}`, graphJSON)
	}
}
