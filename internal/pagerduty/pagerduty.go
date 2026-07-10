package pagerduty

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type WebhookPayload struct {
	Event Event `json:"event"`
}

type Event struct {
	ID           string       `json:"id"`
	EventType    string       `json:"event_type"`
	ResourceType string       `json:"resource_type"`
	OccurredAt   string       `json:"occurred_at"`
	Agent        Agent        `json:"agent"`
	Data         IncidentData `json:"data"`
}

type Agent struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
}

type IncidentData struct {
	ID          string    `json:"id"`
	Number      int       `json:"number"`
	Status      string    `json:"status"`
	IncidentKey string    `json:"incident_key"`
	CreatedAt   string    `json:"created_at"`
	Title       string    `json:"title"`
	Urgency     string    `json:"urgency"`
	Service     Service   `json:"service"`
	Assignees   []User    `json:"assignees"`
	Priority    *Priority `json:"priority"`
}

type Service struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
}

type User struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
}

type Priority struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
}

func IncidentHandler(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var payload WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	fmt.Println("Event Type:", payload.Event.EventType)
	fmt.Println("Incident ID:", payload.Event.Data.ID)
	fmt.Println("Incident Number:", payload.Event.Data.Number)
	fmt.Println("Title:", payload.Event.Data.Title)
	fmt.Println("Status:", payload.Event.Data.Status)
	fmt.Println("Urgency:", payload.Event.Data.Urgency)
	fmt.Println("Service:", payload.Event.Data.Service.Summary)

	if len(payload.Event.Data.Assignees) > 0 {
		fmt.Println("Assigned To:", payload.Event.Data.Assignees[0].Summary)
	}

	if payload.Event.Data.Priority != nil {
		fmt.Println("Priority:", payload.Event.Data.Priority.Summary)
	}

	w.WriteHeader(http.StatusOK)
}
