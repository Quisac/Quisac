package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxWebhookBody caps the payload we read (GitHub deliveries are ≤25 MiB).
const maxWebhookBody = 25 << 20

// commitsCap is GitHub's limit on the push payload's commits array. At or above
// it the changed-file list may be truncated, so the change set is incomplete.
const commitsCap = 20

type pushEvent struct {
	Ref        string `json:"ref"`
	Before     string `json:"before"`
	After      string `json:"after"`
	Repository struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
	Commits []struct {
		Added    []string `json:"added"`
		Removed  []string `json:"removed"`
		Modified []string `json:"modified"`
	} `json:"commits"`
}

// handleWebhook verifies the signature and dispatches push events to the indexer
// asynchronously, so GitHub gets a fast ack.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody))
	if err != nil {
		http.Error(w, "could not read body", http.StatusBadRequest)
		return
	}
	if !verifyWebhookSignature(s.cfg.GitHub.App.WebhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	if r.Header.Get("X-GitHub-Event") == "push" {
		var evt pushEvent
		if err := json.Unmarshal(body, &evt); err != nil {
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}
		go s.processPush(evt)
	}
	w.WriteHeader(http.StatusNoContent)
}

// processPush validates the push targets the default branch and forwards it to
// the indexer.
func (s *Server) processPush(evt pushEvent) {
	repo := evt.Repository.FullName
	if repo == "" || evt.Installation.ID == 0 {
		return
	}
	if evt.Ref != "refs/heads/"+evt.Repository.DefaultBranch {
		return
	}

	installID := strconv.FormatInt(evt.Installation.ID, 10)
	complete := len(evt.Commits) < commitsCap
	changed := changedPaths(evt)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	if err := s.indexer.ProcessPush(ctx, installID, repo, evt.Before, evt.After, changed, complete); err != nil {
		fmt.Printf("webhook %s: %v\n", repo, err)
	}
}

// changedPaths is the de-duplicated union of files touched across the commits.
func changedPaths(evt pushEvent) []string {
	seen := map[string]bool{}
	var out []string
	add := func(ps []string) {
		for _, p := range ps {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	for _, c := range evt.Commits {
		add(c.Added)
		add(c.Modified)
		add(c.Removed)
	}
	return out
}

// verifyWebhookSignature checks X-Hub-Signature-256 against an HMAC of the body.
func verifyWebhookSignature(secret string, body []byte, header string) bool {
	const prefix = "sha256="
	if secret == "" || !strings.HasPrefix(header, prefix) {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(header[len(prefix):]))
}
