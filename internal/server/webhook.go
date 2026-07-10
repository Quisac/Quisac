package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"quisac/internal/db"
	"quisac/internal/worker"
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
		if err := s.enqueuePush(r.Context(), evt); err != nil {
			// Fail the delivery so GitHub retries; the push is not yet durable.
			s.logf(r).Error("enqueue push", "err", err, "repo", evt.Repository.FullName)
			http.Error(w, "could not enqueue", http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// enqueuePush durably queues a push for incremental processing. Irrelevant
// events (non-default-branch, missing repo/installation) are dropped without
// error so GitHub does not retry them. Only a real enqueue failure returns an
// error, which the caller turns into a non-2xx so GitHub retries.
func (s *Server) enqueuePush(ctx context.Context, evt pushEvent) error {
	repo := evt.Repository.FullName
	if repo == "" || evt.Installation.ID == 0 {
		return nil
	}
	if evt.Ref != "refs/heads/"+evt.Repository.DefaultBranch {
		return nil
	}

	payload, err := json.Marshal(worker.PushPayload{
		Before:   evt.Before,
		After:    evt.After,
		Changed:  changedPaths(evt),
		Complete: len(evt.Commits) < commitsCap,
	})
	if err != nil {
		return err
	}

	jobID, err := randomState()
	if err != nil {
		return err
	}
	jobID = jobID[:16]

	if err := s.queue.EnqueueJob(ctx, db.Job{
		ID:             jobID,
		Kind:           db.JobPush,
		Repo:           repo,
		InstallationID: strconv.FormatInt(evt.Installation.ID, 10),
		Payload:        payload,
	}); err != nil {
		return err
	}
	s.waker.Wake()
	return nil
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
