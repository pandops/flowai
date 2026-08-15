package platform

import (
	"encoding/json"
	"time"
)

type UIDashboard struct {
	Period      string              `json:"period"`
	CalendarKey string              `json:"calendar_key"`
	Counts      map[string]int      `json:"counts"`
	Buckets     []UIDashboardBucket `json:"buckets"`
}

type UIDashboardBucket struct {
	At         time.Time      `json:"at"`
	Key        string         `json:"key"`
	Label      string         `json:"label"`
	ShortLabel string         `json:"shortLabel"`
	Total      int            `json:"total"`
	Counts     map[string]int `json:"counts"`
	IsFuture   bool           `json:"isFuture"`
}

type UITask struct {
	TaskID         string            `json:"task_id"`
	TaskTypeID     string            `json:"task_type_id"`
	SourceSystemID string            `json:"source_system_id"`
	SourceID       string            `json:"source_id"`
	CurrentState   string            `json:"current_state"`
	ExecutorID     *string           `json:"executor_id"`
	IngestedAt     time.Time         `json:"ingested_at"`
	ClaimedAt      *time.Time        `json:"claimed_at"`
	CompletedAt    *time.Time        `json:"completed_at"`
	Environment    map[string]string `json:"environment"`
	SecretKeys     []string          `json:"secret_keys"`
}

type UITaskFilter struct {
	Status string
	Period string
	Search string
	Date   string
}

type UITaskPage struct {
	Items      []UITask `json:"items"`
	NextCursor *string  `json:"next_cursor"`
}

type UIEvent struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	Status     string          `json:"status"`
	ExecutorID *string         `json:"executor_id"`
	OccurredAt time.Time       `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

type UIEventPage struct {
	Items      []UIEvent `json:"items"`
	NextCursor *string   `json:"next_cursor"`
}

type UIExecutor struct {
	ExecutorID      string          `json:"executor_id"`
	ExecutorType    string          `json:"executor_type"`
	Scope           string          `json:"scope"`
	AuthorizedTag   string          `json:"authorized_tag"`
	Status          string          `json:"status"`
	MaxCapacity     int             `json:"max_capacity"`
	RunningCount    int             `json:"running_count"`
	Available       int             `json:"available_capacity"`
	RuntimeMetadata json.RawMessage `json:"runtime_metadata"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type UIExecutorFilter struct {
	Status string
	Search string
}

type UIExecutorPage struct {
	Items      []UIExecutor `json:"items"`
	NextCursor *string      `json:"next_cursor"`
}

type UIAuditFilter struct {
	Search string
}

type UIAuditPage struct {
	Items      []AuditEntry `json:"items"`
	NextCursor *string      `json:"next_cursor"`
}
