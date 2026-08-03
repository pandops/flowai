package platform

import "time"

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

type ControlPage struct {
	Items []TaskControl `json:"items"`
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
