// Package pagerduty handles incoming PagerDuty V3 webhook deliveries.
//
// PagerDuty sends a JSON payload to /api/pagerduty/webhook whenever an incident
// is triggered, acknowledged, or resolved. We parse the payload, verify the
// HMAC-SHA256 signature, and hand a normalised Incident to the traversal engine.
package pagerduty

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"quisac/internal/traverse"
)

// ─── Webhook payload types (PagerDuty V3 schema) ─────────────────────────────

// WebhookPayload is the top-level envelope PagerDuty sends.
type WebhookPayload struct {
	Event Event `json:"event"`
}

type Event struct {
	ID           string       `json:"id"`
	EventType    string       `json:"event_type"`    // e.g. "incident.triggered"
	ResourceType string       `json:"resource_type"` // "incident"
	OccurredAt   string       `json:"occurred_at"`
	Agent        Agent        `json:"agent"`
	Data         IncidentData `json:"data"`
}

type Agent struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
}

// IncidentData holds the incident fields. Body.Details is the free-text error
// message / stack trace written by the monitoring system that fired the alert.
type IncidentData struct {
	ID          string        `json:"id"`
	Number      int           `json:"number"`
	Status      string        `json:"status"`
	IncidentKey string        `json:"incident_key"`
	CreatedAt   string        `json:"created_at"`
	Title       string        `json:"title"`
	Urgency     string        `json:"urgency"`
	Service     Service       `json:"service"`
	Assignees   []User        `json:"assignees"`
	Priority    *Priority     `json:"priority"`
	Body        *IncidentBody `json:"body"` // contains the raw error / stack trace
}

// IncidentBody carries the free-text details attached to the PagerDuty alert.
// In PD V3 this is the "details" field set by the events API or Grafana.
type IncidentBody struct {
	Type    string `json:"type"`
	Details string `json:"details"`
}

type Service struct {
	ID      string `json:"id"`
	Summary string `json:"summary"` // human name, e.g. "billing-api"
}

type User struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
}

type Priority struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
}

// ─── Incident extraction ──────────────────────────────────────────────────────

// ExtractIncident converts a raw PagerDuty WebhookPayload into the traverse.Incident
// type consumed by the traversal engine.
func ExtractIncident(p WebhookPayload) traverse.Incident {
	data := p.Event.Data
	errorBody := ""
	if data.Body != nil {
		errorBody = data.Body.Details
	}
	// Also fold the incident title into the error body — it often contains the
	// function name or HTTP path that triggered the alert.
	if data.Title != "" {
		errorBody = data.Title + "\n" + errorBody
	}
	return traverse.Incident{
		ID:          data.ID,
		Title:       data.Title,
		ServiceName: data.Service.Summary,
		ErrorBody:   strings.TrimSpace(errorBody),
	}
}

// ─── Signature verification ───────────────────────────────────────────────────

// VerifySignature checks the PagerDuty V3 HMAC-SHA256 signature.
// PD sends the signature as "v1=<hex>" in the X-PagerDuty-Signature header.
// Returns true when the signature is valid or when secret is empty (dev mode).
func VerifySignature(secret string, body []byte, header string) bool {
	if secret == "" {
		return true // signature checking disabled — only safe in local dev
	}
	// Header may contain multiple signatures separated by commas.
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		val, ok := strings.CutPrefix(part, "v1=")
		if !ok {
			continue
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		want := hex.EncodeToString(mac.Sum(nil))
		if hmac.Equal([]byte(want), []byte(val)) {
			return true
		}
	}
	return false
}

// ─── HTTP handler (plain handler used when no traverser is wired) ─────────────

// IncidentHandler is the minimal fallback handler registered when the server
// has no traverser available. It validates the payload and logs the incident.
// In production, use Server.HandlePagerDuty instead (it runs the full pipeline).
func IncidentHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB cap
	defer r.Body.Close()
	if err != nil {
		http.Error(w, "could not read body", http.StatusBadRequest)
		return
	}

	var payload WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid JSON payload", http.StatusBadRequest)
		return
	}

	inc := ExtractIncident(payload)
	fmt.Printf("[pagerduty] incident=%s type=%s service=%q title=%q\n",
		inc.ID, payload.Event.EventType, inc.ServiceName, inc.Title)
	if inc.ErrorBody != "" {
		preview := inc.ErrorBody
		if len(preview) > 200 {
			preview = preview[:200] + "…"
		}
		fmt.Printf("[pagerduty] error_body=%s\n", preview)
	}

	w.WriteHeader(http.StatusAccepted)
}
