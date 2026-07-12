// Package platform holds cross-cutting types shared by all FlowAI services.
// Concrete implementations live in service-specific packages.
package platform

import "time"

// ExecutorType is the canonical executor type identifier.
const ExecutorTypeDockerOpenHands = "docker-openhands"

// ExecutorRecord is the canonical representation of an active Executor
// instance stored by the State Registry.
type ExecutorRecord struct {
	ExecutorID        string                 `json:"executor_id" yaml:"executor_id"`
	ExecutorType      string                 `json:"executor_type" yaml:"executor_type"`
	RoutingTarget     string                 `json:"routing_target" yaml:"routing_target"`
	Capacity          int                    `json:"capacity" yaml:"capacity"`
	RunningChildCount int                    `json:"running_child_count" yaml:"running_child_count"`
	Metadata          map[string]interface{} `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	RegisteredAt      time.Time              `json:"registered_at" yaml:"registered_at"`
}

// ExecutorEventType enumerates Executor lifecycle event kinds.
type ExecutorEventType string

const (
	ExecutorEventRegistered ExecutorEventType = "executor.registered"
	ExecutorEventStarted    ExecutorEventType = "executor.started"
	ExecutorEventHealthy    ExecutorEventType = "executor.healthy"
	ExecutorEventBusy       ExecutorEventType = "executor.busy"
	ExecutorEventIdle       ExecutorEventType = "executor.idle"
	ExecutorEventStopping   ExecutorEventType = "executor.stopping"
	ExecutorEventStopped    ExecutorEventType = "executor.stopped"
	ExecutorEventFailed     ExecutorEventType = "executor.failed"
)

// ExecutorEvent represents one Executor lifecycle record.
type ExecutorEvent struct {
	EventID    string                 `json:"event_id"`
	ExecutorID string                 `json:"executor_id"`
	Type       ExecutorEventType      `json:"type"`
	OccurredAt time.Time              `json:"occurred_at"`
	Payload    map[string]interface{} `json:"payload,omitempty"`
}

// TaskEventSource enumerates the origin of a task event.
type TaskEventSource string

const (
	TaskSourceExecutor  TaskEventSource = "executor"
	TaskSourceDocker    TaskEventSource = "docker"
	TaskSourceOpenHands TaskEventSource = "openhands"
)

// TaskEvent represents one task lifecycle record.
type TaskEvent struct {
	EventID    string                 `json:"event_id"`
	TaskID     string                 `json:"task_id"`
	ExecutorID string                 `json:"executor_id"`
	Source     TaskEventSource        `json:"source"`
	Type       string                 `json:"type"`
	OccurredAt time.Time              `json:"occurred_at"`
	Payload    map[string]interface{} `json:"payload,omitempty"`
}

// TaskStatus enumerates Router task statuses.
type TaskStatus string

const (
	TaskStatusQueued             TaskStatus = "queued"
	TaskStatusRunning            TaskStatus = "running"
	TaskStatusInterruptRequested TaskStatus = "interrupt_requested"
	TaskStatusMessagePending     TaskStatus = "message_pending"
	TaskStatusCompleted          TaskStatus = "completed"
	TaskStatusFailed             TaskStatus = "failed"
)

// PendingActionType enumerates Router-provided pending actions.
type PendingActionType string

const (
	ActionInterruptTask     PendingActionType = "interrupt_task"
	ActionAppendTaskMessage PendingActionType = "append_task_message"
)

// InterruptTaskAction requests that the Executor interrupt an in-flight task.
type InterruptTaskAction struct {
	ActionID string `json:"action_id"`
	Type     string `json:"type"`
	TaskID   string `json:"task_id"`
	Reason   string `json:"reason,omitempty"`
}

// AppendTaskMessageAction requests that the Executor forward a message to a running task.
type AppendTaskMessageAction struct {
	ActionID string `json:"action_id"`
	Type     string `json:"type"`
	TaskID   string `json:"task_id"`
	Content  string `json:"content"`
	Role     string `json:"role,omitempty"`
}

// RouterTask is the wire shape returned by the Router task list endpoint.
type RouterTask struct {
	TaskID         string                 `json:"task_id"`
	RoutingTarget  string                 `json:"routing_target"`
	AgentRuntime   string                 `json:"agent_runtime"`
	Status         TaskStatus             `json:"status"`
	Prompt         string                 `json:"prompt"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	PendingActions []PendingAction        `json:"pending_actions,omitempty"`
}

// PendingAction is a tagged union of router pending actions.
type PendingAction struct {
	Type     PendingActionType `json:"type"`
	ActionID string            `json:"action_id,omitempty"`
	TaskID   string            `json:"task_id,omitempty"`
	Reason   string            `json:"reason,omitempty"`
	Content  string            `json:"content,omitempty"`
	Role     string            `json:"role,omitempty"`
}

// Action is the discriminator union of router action shapes.
type Action struct {
	// InterruptTask fields
	ActionID string `json:"action_id,omitempty"`
	TaskID   string `json:"task_id,omitempty"`
	Reason   string `json:"reason,omitempty"`
	// AppendTaskMessage fields
	Content string `json:"content,omitempty"`
	Role    string `json:"role,omitempty"`
}

// ExecutorRegistrationResponse is returned by PUT /v1/executors/{executor_id}.
type ExecutorRegistrationResponse struct {
	ExecutorID   string    `json:"executor_id"`
	RegisteredAt time.Time `json:"registered_at"`
}

// EventAppendResponse is returned by all event-append endpoints.
type EventAppendResponse struct {
	EventID    string    `json:"event_id"`
	AcceptedAt time.Time `json:"accepted_at"`
}

// OpenEnvResponse is returned by GET /v1/env.
type OpenEnvResponse struct {
	Values map[string]string `json:"values"`
}

// TaskListResponse is returned by GET /v1/tasks.
type TaskListResponse struct {
	Tasks []RouterTask `json:"tasks"`
}

// LivenessResponse is returned by GET /v1/livez.
type LivenessResponse struct {
	Status     string `json:"status"`
	ExecutorID string `json:"executor_id,omitempty"`
}

// ReadinessResponse is returned by GET /v1/readyz.
type ReadinessResponse struct {
	Status                  string `json:"status"`
	ExecutorID              string `json:"executor_id,omitempty"`
	StateRegistryRegistered bool   `json:"state_registry_registered"`
	OpenHandsReachable      bool   `json:"openhands_reachable"`
}
