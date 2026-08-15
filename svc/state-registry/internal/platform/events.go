package platform

import (
	"encoding/json"
	"time"
)

// TaskEventAppendRequest is the closed request body for
// POST /v1/tasks/{task_id}/events. The envelope carries the full
// required shape: event_id, team_id, task_id, executor_id, event_type,
// occurred_at, and payload. An identical (task_id, event_id) retry
// returns the original 202 body without appending a duplicate and
// without changing the projected task state.
type TaskEventAppendRequest struct {
	EventID    string          `json:"event_id"`
	TeamID     string          `json:"team_id"`
	TaskID     string          `json:"task_id"`
	ExecutorID string          `json:"executor_id"`
	EventType  string          `json:"event_type"`
	OccurredAt string          `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

// ExecutorEventAppendRequest is the closed request body for
// POST /v1/executors/{executor_id}/events. When the Executor's
// scope = team, the envelope team_id SHALL equal the Executor's
// immutable team_id; when scope = system, the envelope team_id SHALL
// be null or absent. task_id is optional and is supported for
// per-task self events but never gates the append.
type ExecutorEventAppendRequest struct {
	EventID    string          `json:"event_id"`
	TeamID     *string         `json:"team_id"`
	TaskID     *string         `json:"task_id"`
	ExecutorID string          `json:"executor_id"`
	EventType  string          `json:"event_type"`
	OccurredAt string          `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

// EventAcceptance is the documented 202 response for both task
// events and executor self events. The accepted_at timestamp is the
// wire-format RFC3339Nano UTC value the registry persists on the row.
type EventAcceptance struct {
	EventID    string `json:"event_id"`
	TeamID     string `json:"team_id"`
	AcceptedAt string `json:"accepted_at"`
}

// TaskTerminalEventTypes enumerates the post-create Executor-emitted
// task event types. The first lifecycle event `created` is
// Registry-appended on claim and is NEVER accepted on this endpoint.
const (
	TaskEventTypeRunning  = "running"
	TaskEventTypeFinished = "finished"
	TaskEventTypeFailed   = "failed"
)

// ExecutorEventType enumerates the documented self-event envelope
// values. The values that update capacity observations are
// capacity_observed and running_count_observed.
const (
	ExecutorEventTypeRegistered           = "registered"
	ExecutorEventTypeStarted              = "started"
	ExecutorEventTypeHealthy              = "healthy"
	ExecutorEventTypeBusy                 = "busy"
	ExecutorEventTypeIdle                 = "idle"
	ExecutorEventTypeStopping             = "stopping"
	ExecutorEventTypeStopped              = "stopped"
	ExecutorEventTypeFailed               = "failed"
	ExecutorEventTypeCapacityObserved     = "capacity_observed"
	ExecutorEventTypeRunningCountObserved = "running_count_observed"
)

// ExecutorEvent is one immutable canonical Executor self event.
type ExecutorEvent struct {
	EventID    string          `json:"event_id"`
	TeamID     *string         `json:"team_id"`
	TaskID     *string         `json:"task_id"`
	ExecutorID string          `json:"executor_id"`
	EventType  string          `json:"event_type"`
	OccurredAt string          `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

// ExecutorEventPage is the ordered, team-filtered self-event response.
type ExecutorEventPage struct {
	Items []ExecutorEvent `json:"items"`
	Page  AdminPageInfo   `json:"page"`
}

// TerminalSnapshot is the in-memory helper for the strict-ordering
// (occurred_at, event_id) tuple comparison performed by the store.
type TerminalSnapshot struct {
	OccurredAt time.Time
	EventID    string
}

type UIStreamFrame struct {
	FrameID    string          `json:"frame_id"`
	TeamID     string          `json:"team_id"`
	FrameType  string          `json:"frame_type"`
	TaskID     *string         `json:"task_id"`
	ControlID  *string         `json:"control_id"`
	OccurredAt string          `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
}
