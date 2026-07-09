package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// readinessCheck is a named dependency probe run by /readyz.
type readinessCheck struct {
	name  string
	check func(ctx context.Context) error
}

// AddReadinessCheck registers a dependency probe (e.g. Postgres, Neo4j) that
// /readyz will run. Call before serving; it is not safe for concurrent use.
func (s *Server) AddReadinessCheck(name string, check func(ctx context.Context) error) {
	s.checks = append(s.checks, readinessCheck{name: name, check: check})
}

// handleHealthz is a liveness probe: it returns 200 as long as the process can
// serve HTTP. It intentionally does not touch dependencies.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleReadyz is a readiness probe: it runs every registered dependency check
// and returns 503 if any fails, so a load balancer can route around an instance
// that has lost its database or graph store.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	results := make(map[string]string, len(s.checks))
	ready := true
	for _, c := range s.checks {
		if err := c.check(ctx); err != nil {
			ready = false
			results[c.name] = "error: " + err.Error()
			s.logf(r).Error("readiness check failed", "check", c.name, "err", err)
		} else {
			results[c.name] = "ok"
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if !ready {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	json.NewEncoder(w).Encode(map[string]any{"ready": ready, "checks": results})
}
