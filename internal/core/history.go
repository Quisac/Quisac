package core

import "time"

// FileChurn seeds the change-history signals from a repository's commit history
// when a repo is first indexed, before any push webhook has been seen.
type FileChurn struct {
	Path          string    `json:"path"`
	Changes       int       `json:"changes"`
	LastChangedAt time.Time `json:"last_changed_at"`
}

// FileStat is a file's accumulated change and fault history: churn and recency
// from commit activity, faults from resolutions that shipped a fix touching it.
// It is the "has this been trouble before?" input to root-cause ranking.
type FileStat struct {
	ChangeCount   int       `json:"change_count"`
	LastChangedAt time.Time `json:"last_changed_at"`
	FaultCount    int       `json:"fault_count"`
	LastFaultAt   time.Time `json:"last_fault_at"`
}
