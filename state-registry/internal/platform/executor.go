package platform

import (
	"encoding/json"
	"time"
)

const (
	ExecutorScopeTeam   = "team"
	ExecutorScopeSystem = "system"
)

// ExecutorIdentity is the authenticated service binding supplied by the
// transport adapter. TeamID is nil for a system-owned Executor.
type ExecutorIdentity struct {
	ExecutorID string
	Scope      string
	TeamID     *string
	RequestID  string
}

// ExecutorRegistrationRequest is the closed request body for
// PUT /v1/executors/{executor_id}.
type ExecutorRegistrationRequest struct {
	Scope           string          `json:"scope"`
	TeamID          *string         `json:"team_id"`
	ExecutorType    string          `json:"executor_type"`
	Identity        string          `json:"identity"`
	AuthorizedTag   string          `json:"authorized_tag"`
	MaxCapacity     int             `json:"max_capacity"`
	RunningCount    int             `json:"running_count"`
	RuntimeMetadata json.RawMessage `json:"runtime_metadata"`
}

// Executor is the canonical registered Executor resource.
type Executor struct {
	ExecutorID      string          `json:"executor_id"`
	Scope           string          `json:"scope"`
	TeamID          *string         `json:"team_id"`
	ExecutorType    string          `json:"executor_type"`
	Identity        string          `json:"identity"`
	AuthorizedTag   string          `json:"authorized_tag"`
	MaxCapacity     int             `json:"max_capacity"`
	RunningCount    int             `json:"running_count"`
	RuntimeMetadata json.RawMessage `json:"runtime_metadata"`
	RegisteredAt    time.Time       `json:"registered_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// TaskSummary is the exact read-only discovery projection.
type TaskSummary struct {
	TaskID       string    `json:"task_id"`
	TeamID       string    `json:"team_id"`
	RequiredTag  string    `json:"required_tag"`
	CurrentState string    `json:"current_state"`
	IngestedAt   time.Time `json:"ingested_at"`
}

// TaskDiscoveryPage is returned only when at least one task is eligible.
type TaskDiscoveryPage struct {
	Items []TaskSummary `json:"items"`
}
