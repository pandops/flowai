package platform

import "encoding/json"

// ListenerIdentity is the authenticated listener context derived from the
// header adapter mounted only in explicit test mode. TeamID and
// SourceSystemID are immutable for the lifetime of the listener principal;
// the registry treats the supplied value as the authoritative team and
// source-system binding for every ingestion request. RequestID correlates
// the write without exposing transport authentication details.
type ListenerIdentity struct {
	TeamID         string
	SourceSystemID string
	Identity       string
	RequestID      string
}

// TaskIngestionRequest is the closed request body documented by the
// OpenAPI TaskIngestionRequest schema. The field set is the documented
// listener-time surface; `required_tag` is intentionally absent because
// the registry derives it from the referenced task type's
// `execution_tag` and SHALL NOT accept a listener-supplied authoritative
// value. Optional fields (project_id, environment_id, image) are
// pointer-shaped so the listener may omit them; missing entries render
// as null on the canonical task row.
type TaskIngestionRequest struct {
	TeamID         string          `json:"team_id"`
	SourceSystemID string          `json:"source_system_id"`
	SourceID       string          `json:"source_id"`
	TaskTypeID     string          `json:"task_type_id"`
	Payload        json.RawMessage `json:"payload"`
	ProjectID      *string         `json:"project_id,omitempty"`
	EnvironmentID  *string         `json:"environment_id,omitempty"`
	Image          *ImageReference `json:"image,omitempty"`
}
