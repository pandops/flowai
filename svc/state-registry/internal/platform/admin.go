package platform

import "time"

// AdminIdentity is the authenticated system-administrator context passed to
// persistence. Subject identifies the administrator and RequestID correlates
// the write without exposing transport authentication details.
type AdminIdentity struct {
	Subject   string
	RequestID string
}

// ImageReference identifies an immutable container image.
type ImageReference struct {
	Repository string `json:"repository"`
	Digest     string `json:"digest"`
}

// CreateTeamRequest is the closed request body for POST /admin/teams.
type CreateTeamRequest struct {
	TeamName     string `json:"team_name"`
	DefaultImage string `json:"default_image"`
}

// Team is the canonical team resource returned after creation.
type Team struct {
	TeamID       string     `json:"team_id"`
	TeamName     string     `json:"team_name"`
	DefaultImage string     `json:"default_image"`
	IngestedAt   time.Time  `json:"ingested_at"`
	ArchivedAt   *time.Time `json:"archived_at"`
}

// UpdateTeamRequest changes presentation metadata without changing ownership.
type UpdateTeamRequest struct {
	TeamName     *string `json:"team_name,omitempty"`
	DefaultImage *string `json:"default_image,omitempty"`
}

// CreateSourceSystemRequest is the closed request body for
// POST /admin/source-systems.
type CreateSourceSystemRequest struct {
	TeamID           string          `json:"team_id"`
	ListenerIdentity string          `json:"listener_identity"`
	DefaultImage     *ImageReference `json:"default_image,omitempty"`
}

// SourceSystem is the canonical source-system resource returned after
// creation. DefaultImage is nil when no source-system default was supplied.
type SourceSystem struct {
	SourceSystemID   string          `json:"source_system_id"`
	TeamID           string          `json:"team_id"`
	ListenerIdentity string          `json:"listener_identity"`
	DefaultImage     *ImageReference `json:"default_image"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// CreateTaskTypeRequest is the closed request body for POST /admin/task-types.
type CreateTaskTypeRequest struct {
	TeamID       string          `json:"team_id"`
	ExecutionTag string          `json:"execution_tag"`
	DefaultImage *ImageReference `json:"default_image,omitempty"`
}

// TaskType is the canonical task-type resource returned after creation.
// DefaultImage is nil when no task-type default was supplied.
type TaskType struct {
	TaskTypeID   string          `json:"task_type_id"`
	TeamID       string          `json:"team_id"`
	ExecutionTag string          `json:"execution_tag"`
	DefaultImage *ImageReference `json:"default_image"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}
