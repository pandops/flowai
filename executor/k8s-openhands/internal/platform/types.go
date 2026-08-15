// Package platform holds cross-cutting types shared inside the
// executor_k8s_openhands service. Concrete implementations live in
// service-specific packages. Per AGENTS.md, the State Registry does
// not export types to sibling Executors; this package duplicates the
// v0002/v0005 wire types verbatim and pins the concrete wire value
// to executor_k8s_openhands.
package platform

import (
	"encoding/json"
	"time"
)

const (
	ExecutorScopeTeam   = "team"
	ExecutorScopeSystem = "system"
)

// ExecutorType is the canonical concrete executor type identifier.
// The wire value follows executor_<runtime>_<tool> documented in
// AGENTS.md and matches the concrete service directory. The Go
// identifier preserves the runtime/tool axis; both refer to the
// same concrete Executor.
const ExecutorTypeK8sOpenHands = "executor_k8s_openhands"

// ExecutorRegistrationResponse is returned by PUT /v1/executors/{executor_id}.
type ExecutorRegistrationResponse struct {
	ExecutorID      string                 `json:"executor_id"`
	Scope           string                 `json:"scope"`
	TeamID          *string                `json:"team_id"`
	ExecutorType    string                 `json:"executor_type"`
	Identity        string                 `json:"identity"`
	AuthorizedTag   string                 `json:"authorized_tag"`
	MaxCapacity     int                    `json:"max_capacity"`
	RunningCount    int                    `json:"running_count"`
	RuntimeMetadata map[string]interface{} `json:"runtime_metadata"`
	RegisteredAt    time.Time              `json:"registered_at"`
	UpdatedAt       time.Time              `json:"updated_at"`
}

// StateRegistryExecutorRegistration is the v0002/v0005 registration
// body sent to the durable State Registry. The v0005 contract
// requires exactly one scope, exactly one authorized_tag, exactly
// one identity-bound team_id for team scope, observed max_capacity,
// observed running_count, and runtime metadata.
type StateRegistryExecutorRegistration struct {
	Scope           string          `json:"scope"`
	TeamID          *string         `json:"team_id"`
	ExecutorType    string          `json:"executor_type"`
	Identity        string          `json:"identity"`
	AuthorizedTag   string          `json:"authorized_tag"`
	MaxCapacity     int             `json:"max_capacity"`
	RunningCount    int             `json:"running_count"`
	RuntimeMetadata json.RawMessage `json:"runtime_metadata"`
}

// v0005 task event type values for Executor-emitted lifecycle
// events. The first lifecycle event (`created`) is Registry-appended
// on claim and is NEVER sent by Executors.
const (
	TaskEventTypeRunning  = "running"
	TaskEventTypeFinished = "finished"
	TaskEventTypeFailed   = "failed"
)

// V0002ExecutorSelfEventType enumerates the Executor self-event
// envelope values.
type V0002ExecutorSelfEventType string

const (
	V0002ExecutorEventHealthy  V0002ExecutorSelfEventType = "healthy"
	V0002ExecutorEventBusy     V0002ExecutorSelfEventType = "busy"
	V0002ExecutorEventIdle     V0002ExecutorSelfEventType = "idle"
	V0002ExecutorEventStopping V0002ExecutorSelfEventType = "stopping"
	V0002ExecutorEventStopped  V0002ExecutorSelfEventType = "stopped"
	V0002ExecutorEventFailed   V0002ExecutorSelfEventType = "failed"
)

// V0002 transport headers per the v0005 spec. The State Registry
// treats X-FlowAI-Executor-Id and X-FlowAI-Team-Id as trusted request
// data; after v0009 the backend does not authenticate the Executor
// connection so the deployment network policy owns the caller
// boundary.
const (
	V0002HeaderExecutorID = "X-FlowAI-Executor-Id"
	V0002HeaderTeamID     = "X-FlowAI-Team-Id"
	V0002HeaderRole       = "X-FlowAI-Role"
	V0002HeaderRoleTeam   = "team-executor"
	V0002HeaderRoleSystem = "system-executor"
)

// V0002ErrorResponse mirrors the documented error body shape.
type V0002ErrorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// V0002ImageReference mirrors the documented ImageReference.
type V0002ImageReference struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag,omitempty"`
	Digest     string `json:"digest,omitempty"`
}

// V0002TaskListEntry mirrors the canonical TaskListEntry.
type V0002TaskListEntry struct {
	TaskID         string               `json:"task_id"`
	TeamID         string               `json:"team_id"`
	SourceSystemID string               `json:"source_system_id"`
	SourceID       string               `json:"source_id"`
	TaskTypeID     string               `json:"task_type_id"`
	RequiredTag    string               `json:"required_tag"`
	Payload        json.RawMessage      `json:"payload"`
	CurrentState   string               `json:"current_state"`
	OwnerCommandID *string              `json:"owner_command_id"`
	ExecutorID     *string              `json:"executor_id"`
	ProjectID      *string              `json:"project_id"`
	EnvironmentID  *string              `json:"environment_id"`
	Image          *V0002ImageReference `json:"image"`
	ResolvedImage  *V0002ImageReference `json:"resolved_image"`
	ImageSource    *string              `json:"image_source"`
	IngestedAt     string               `json:"ingested_at"`
	ClaimedAt      *string              `json:"claimed_at"`
}

// V0002TaskSummary is the read-only discovery projection.
type V0002TaskSummary struct {
	TaskID       string    `json:"task_id"`
	TeamID       string    `json:"team_id"`
	RequiredTag  string    `json:"required_tag"`
	CurrentState string    `json:"current_state"`
	IngestedAt   time.Time `json:"ingested_at"`
}

// V0002TaskDiscoveryPage is the documented discovery response.
type V0002TaskDiscoveryPage struct {
	Items []V0002TaskSummary `json:"items"`
}

// V0002ClaimRequest is the documented claim body.
type V0002ClaimRequest struct {
	TaskID    string `json:"task_id"`
	CommandID string `json:"command_id"`
}

// V0002ClaimResponse is the documented 200 envelope returned by
// POST /v1/executors/{executor_id}/claim. The State Registry issues
// the compact three-part scope_token together with the canonical
// task projection; both are required for the EnvironmentID open path.
type V0002ClaimResponse struct {
	Claim            string               `json:"claim"`
	Task             V0002TaskListEntry   `json:"task"`
	ResolvedImage    *V0002ImageReference `json:"resolved_image"`
	ImageSource      *string              `json:"image_source"`
	ClaimedAt        *string              `json:"claimed_at"`
	EnvironmentID    *string              `json:"environment_id"`
	LaunchParameters bool                 `json:"launch_parameters"`
	ScopeToken       *string              `json:"scope_token"`
}

// V0002TaskEventEnvelope is the closed request body for
// POST /v1/tasks/{task_id}/events.
type V0002TaskEventEnvelope struct {
	EventID    string          `json:"event_id"`
	TeamID     string          `json:"team_id"`
	TaskID     string          `json:"task_id"`
	ExecutorID string          `json:"executor_id"`
	EventType  string          `json:"event_type"`
	OccurredAt string          `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

// V0002ExecutorEventEnvelope is the closed request body for
// POST /v1/executors/{executor_id}/events.
type V0002ExecutorEventEnvelope struct {
	EventID    string          `json:"event_id"`
	TeamID     *string         `json:"team_id"`
	TaskID     *string         `json:"task_id"`
	ExecutorID string          `json:"executor_id"`
	EventType  string          `json:"event_type"`
	OccurredAt string          `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

// V0002EventAcceptance is the 202 response for both event routes.
type V0002EventAcceptance struct {
	EventID    string `json:"event_id"`
	TeamID     string `json:"team_id"`
	AcceptedAt string `json:"accepted_at"`
}

const (
	TaskLogStreamWork      = "work"
	TaskLogStreamReasoning = "reasoning"
)

// TaskLogAppendRequest is one explicitly published OpenHands output chunk.
// Reasoning means agent-published assistant text, never hidden model state.
type TaskLogAppendRequest struct {
	LogChunkID string    `json:"log_chunk_id"`
	Stream     string    `json:"stream"`
	Content    string    `json:"content"`
	OccurredAt time.Time `json:"occurred_at"`
}

// V0002OpenEnvironmentResponse is the documented 200 envelope
// returned by GET /v1/environments/{environment_id}/open.
type V0002OpenEnvironmentResponse struct {
	TeamID     string            `json:"team_id"`
	TaskID     string            `json:"task_id"`
	ExecutorID string            `json:"executor_id"`
	Values     map[string]string `json:"values"`
}

// V0002TaskControl is one immutable canonical control record.
type V0002TaskControl struct {
	ControlID      string    `json:"control_id"`
	TeamID         string    `json:"team_id"`
	TaskID         string    `json:"task_id"`
	OperatorID     string    `json:"operator_id"`
	Action         string    `json:"action"`
	IdempotencyKey string    `json:"idempotency_key"`
	Status         string    `json:"status"`
	AuditID        string    `json:"audit_id"`
	RequestedAt    time.Time `json:"requested_at"`
}

type TaskControlEventAppendRequest struct {
	ControlEventID string          `json:"control_event_id"`
	Status         string          `json:"status"`
	OccurredAt     time.Time       `json:"occurred_at"`
	Payload        json.RawMessage `json:"payload"`
}

// V0002TaskControlPage is the response for GET /v1/tasks/{task_id}/controls.
type V0002TaskControlPage struct {
	Items []V0002TaskControl `json:"items"`
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
	KubeReachable           bool   `json:"kube_reachable"`
}
