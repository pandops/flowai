package platform

import (
	"encoding/json"
	"time"
)

type GatewayIdentity struct {
	TeamID     string
	OperatorID string
	RequestID  string
}

type CreateTaskControlRequest struct {
	Action         string  `json:"action"`
	IdempotencyKey string  `json:"idempotency_key"`
	Reason         *string `json:"reason,omitempty"`
}

type TaskControl struct {
	ControlID      string    `json:"control_id"`
	TeamID         string    `json:"team_id"`
	TaskID         string    `json:"task_id"`
	OperatorID     string    `json:"operator_id"`
	Action         string    `json:"action"`
	IdempotencyKey string    `json:"idempotency_key"`
	Reason         *string   `json:"-"`
	Status         string    `json:"status"`
	AuditID        string    `json:"audit_id"`
	RequestedAt    time.Time `json:"requested_at"`
}

type Control struct {
	ControlID string `json:"control_id"`
	Status    string `json:"status"`
}

type ControlPage struct {
	Items []TaskControl `json:"items"`
}

const TaskControlEventTypeRequested = "task.control.requested"

type TaskControlEventAppendRequest struct {
	ControlEventID string          `json:"control_event_id"`
	Status         string          `json:"status"`
	OccurredAt     time.Time       `json:"occurred_at"`
	Payload        json.RawMessage `json:"payload"`
}

type TaskControlEvent struct {
	ControlEventID string          `json:"control_event_id"`
	ControlID      string          `json:"control_id"`
	TaskID         string          `json:"task_id"`
	TeamID         string          `json:"team_id"`
	ExecutorID     *string         `json:"executor_id"`
	EventType      string          `json:"event_type"`
	Status         string          `json:"status"`
	OccurredAt     time.Time       `json:"occurred_at"`
	Payload        json.RawMessage `json:"payload"`
}

type TaskControlEventPage struct {
	Items []TaskControlEvent `json:"items"`
}

type AuditEntry struct {
	AuditID      string    `json:"audit_id"`
	TeamID       string    `json:"team_id"`
	ActorID      string    `json:"actor_id"`
	ActorType    string    `json:"actor_type"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	RequestID    string    `json:"request_id"`
	Outcome      string    `json:"outcome"`
	OccurredAt   time.Time `json:"occurred_at"`
}

type AuditPage struct {
	Items []AuditEntry  `json:"items"`
	Page  AdminPageInfo `json:"page"`
}
