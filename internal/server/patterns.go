package server

import (
	"encoding/json"
	"net/http"

	"quisac/internal/db"
	"quisac/internal/patch"
)

// patternRow is the JSON shape the Patterns view renders. The stored edits are
// not exposed: a reader wants which files a fix touched and how often it held,
// not before/after strings only patch.Apply can use.
type patternRow struct {
	ID          int64    `json:"id"`
	Repo        string   `json:"repo"`
	ServiceName string   `json:"service_name,omitempty"`
	Signature   string   `json:"signature"`
	Fingerprint string   `json:"fingerprint"`
	RootCause   string   `json:"root_cause,omitempty"`
	FixSummary  string   `json:"fix_summary,omitempty"`
	Files       []string `json:"files"`
	// The gap between them is the hit rate the view shows.
	TimesSeen    int    `json:"times_seen"`
	TimesApplied int    `json:"times_applied"`
	CreatedAt    string `json:"created_at"`
	LastSeenAt   string `json:"last_seen_at"`
}

func toPatternRow(p db.Pattern) patternRow {
	row := patternRow{
		ID:           p.ID,
		Repo:         p.Repo,
		ServiceName:  p.ServiceName,
		Signature:    p.Signature,
		Fingerprint:  p.Fingerprint,
		RootCause:    p.RootCause,
		FixSummary:   p.FixSummary,
		Files:        []string{},
		TimesSeen:    p.TimesSeen,
		TimesApplied: p.TimesApplied,
		CreatedAt:    p.CreatedAt.Format("2006-01-02T15:04:05Z"),
		LastSeenAt:   p.LastSeenAt.Format("2006-01-02T15:04:05Z"),
	}
	// A pattern whose edits no longer decode is still listed, just with no files.
	var edits []patch.Edit
	if err := json.Unmarshal(p.Edits, &edits); err == nil {
		if paths := patch.Paths(edits); len(paths) > 0 {
			row.Files = paths
		}
	}
	return row
}

// handlePatterns serves GET /api/patterns: the installation's learned fixes,
// most recently seen first.
func (s *Server) handlePatterns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sd, ok := s.session(r)
	if !ok {
		http.Error(w, "not connected", http.StatusUnauthorized)
		return
	}
	if s.patterns == nil {
		http.Error(w, "pattern store unavailable", http.StatusServiceUnavailable)
		return
	}

	patterns, err := s.patterns.ListPatterns(r.Context(), sd.InstallationID, 0)
	if err != nil {
		s.logf(r).Error("list patterns", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	out := make([]patternRow, 0, len(patterns))
	for _, p := range patterns {
		out = append(out, toPatternRow(p))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
