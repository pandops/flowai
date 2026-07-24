package platform

import "encoding/json"

// AdminTagEntry is the exact 3-field allowlist projection of a
// registered task type returned by GET /admin/tags. The Registry SHALL
// NOT add, omit, rename, or re-order these fields; SHALL NOT include
// `default_image`; and SHALL NOT include any other column from any
// table. Image strings are not team-owned resources and equal
// `execution_tag` values across teams grant no cross-team authority.
type AdminTagEntry struct {
	TeamID       string `json:"team_id"`
	TaskTypeID   string `json:"task_type_id"`
	ExecutionTag string `json:"execution_tag"`
}

// AdminTaskEntry is the exact 11-field allowlist projection of a
// canonical task returned by GET /admin/tasks. The Registry SHALL NOT
// add, omit, rename, or re-order these fields; SHALL NOT include
// `image`, `resolved_image`, `image_source`, `project_id`,
// `environment_id`, or any task payload; and SHALL NOT include any
// other column from any table. Equal `required_tag` strings across
// teams SHALL NOT grant cross-team authority.
type AdminTaskEntry struct {
	TaskID         string  `json:"task_id"`
	TeamID         string  `json:"team_id"`
	TaskTypeID     string  `json:"task_type_id"`
	SourceSystemID string  `json:"source_system_id"`
	SourceID       string  `json:"source_id"`
	RequiredTag    string  `json:"required_tag"`
	CurrentState   string  `json:"current_state"`
	OwnerCommandID *string `json:"owner_command_id"`
	ExecutorID     *string `json:"executor_id"`
	IngestedAt     string  `json:"ingested_at"`
	ClaimedAt      *string `json:"claimed_at"`
}

// TaskState enumerates the canonical current_state values that
// State Registry accepts on writes, projects onto the canonical task
// row, and accepts on collection filters.
const (
	TaskStatePending  = "pending"
	TaskStateCreated  = "created"
	TaskStateRunning  = "running"
	TaskStateFinished = "finished"
	TaskStateFailed   = "failed"
)

// ImageSource enumerates the documented `image_source` values that
// the canonical task row carries after a successful claim. The
// constants match the OpenAPI `ImageSource` enum verbatim.
const (
	ImageSourceTaskOverride        = "task_override"
	ImageSourceTaskTypeDefault     = "task_type_default"
	ImageSourceSourceSystemDefault = "source_system_default"
	ImageSourceTeamDefault         = "team_default"
)

// TaskListEntry is the canonical full task row returned by the
// repository and rendered, without field loss, as the Gateway
// collection item. The struct intentionally exposes every column the
// OpenAPI `Task` schema declares (payload, project_id, environment_id,
// image, resolved_image, image_source) so GET /v1/tasks mirrors the
// documented contract. Admin callers receive an exact 11-field
// projection derived from this canonical row at the HTTP boundary.
type TaskListEntry struct {
	TaskID         string          `json:"task_id"`
	TeamID         string          `json:"team_id"`
	SourceSystemID string          `json:"source_system_id"`
	SourceID       string          `json:"source_id"`
	TaskTypeID     string          `json:"task_type_id"`
	RequiredTag    string          `json:"required_tag"`
	Payload        json.RawMessage `json:"payload"`
	CurrentState   string          `json:"current_state"`
	OwnerCommandID *string         `json:"owner_command_id"`
	ExecutorID     *string         `json:"executor_id"`
	ProjectID      *string         `json:"project_id"`
	EnvironmentID  *string         `json:"environment_id"`
	Image          *ImageReference `json:"image"`
	ResolvedImage  *ImageReference `json:"resolved_image"`
	ImageSource    *string         `json:"image_source"`
	IngestedAt     string          `json:"ingested_at"`
	ClaimedAt      *string         `json:"claimed_at"`
}

// ToAdminTaskEntry projects the canonical task row onto the exact
// 11-field AdminTaskEntry allowlist. The handler boundary is the
// only place that may select AdminTaskEntry columns; the canonical
// row never drops fields before reaching the handler so the Gateway
// collection can be assembled from the same shape.
func (e TaskListEntry) ToAdminTaskEntry() AdminTaskEntry {
	return AdminTaskEntry{
		TaskID:         e.TaskID,
		TeamID:         e.TeamID,
		TaskTypeID:     e.TaskTypeID,
		SourceSystemID: e.SourceSystemID,
		SourceID:       e.SourceID,
		RequiredTag:    e.RequiredTag,
		CurrentState:   e.CurrentState,
		OwnerCommandID: e.OwnerCommandID,
		ExecutorID:     e.ExecutorID,
		IngestedAt:     e.IngestedAt,
		ClaimedAt:      e.ClaimedAt,
	}
}

// GatewayTaskEntry is the team-scoped full-task item returned by
// GET /v1/tasks. The trusted Gateway forwards the verified immutable
// team_id; the Registry applies the team predicate before any cursor
// or order shaping, then renders the canonical TaskListEntry verbatim
// so the response honors the documented OpenAPI Task schema.
type GatewayTaskEntry = TaskListEntry

// AdminPageInfo is the documented pagination envelope appended to every
// paginated admin page. The `next_cursor` is opaque to the caller; a
// nil value means the response is the final page.
type AdminPageInfo struct {
	NextCursor *string `json:"next_cursor"`
	Count      int     `json:"count"`
}

// AdminTagPage is the paginated response shape for GET /admin/tags.
// Items is ordered by (team_id ASC, execution_tag ASC, task_type_id ASC).
type AdminTagPage struct {
	Items []AdminTagEntry `json:"items"`
	Page  AdminPageInfo   `json:"page"`
}

// AdminTaskPage is the paginated response shape for GET /admin/tasks.
// Items is ordered by (ingested_at DESC, task_id DESC).
type AdminTaskPage struct {
	Items []AdminTaskEntry `json:"items"`
	Page  AdminPageInfo    `json:"page"`
}

// GatewayTaskPage is the paginated response shape for GET /v1/tasks.
// Items is ordered by (ingested_at DESC, task_id DESC) under the
// trusted team_id predicate.
type GatewayTaskPage struct {
	Items []GatewayTaskEntry `json:"items"`
	Page  AdminPageInfo      `json:"page"`
}

// AdminTagFilter captures the accepted query filters for
// GET /admin/tags. Empty strings mean "no filter on this column".
type AdminTagFilter struct {
	TeamID string
	Tag    string
}

// AdminTaskFilter captures the accepted query filters for
// GET /admin/tasks. Empty strings mean "no filter on this column".
// State values are restricted to the canonical task states.
type AdminTaskFilter struct {
	TeamID     string
	State      string
	Tag        string
	TaskTypeID string
}

// GatewayTaskFilter captures the accepted query filters for
// GET /v1/tasks. The team_id is NOT taken from the caller; the
// trusted Gateway identity sets it and the Registry applies the
// equality predicate before any pagination, ordering, or cursor work.
// `task_type_id` is intentionally absent; the documented
// /v1/tasks surface is scoped to state and tag filters only.
type GatewayTaskFilter struct {
	State string
	Tag   string
}
