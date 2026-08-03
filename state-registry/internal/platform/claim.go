package platform

// ClaimRequest is the closed request body documented by the OpenAPI
// `ClaimRequest` schema. The field set is exactly `task_id` and
// `command_id`; the Registry rejects every body that carries additional
// fields with `400 invalid_request` so the contract surface is
// narrow and observed end-to-end.
type ClaimRequest struct {
	TaskID    string `json:"task_id"`
	CommandID string `json:"command_id"`
}

// ClaimResponse is the documented success envelope for
// `POST /v1/executors/{executor_id}/claim`. It carries the immutable
// claim state the Registry produced atomically: the canonical task
// row, the four-level-precedence resolved image, the recorded source,
// and the commit-time `claimed_at`. The literal value of the
// `claim` discriminator is the string `"claimed"`; the handler
// retains that field so the wire shape can be checked at the
// boundary even when added fields are introduced.
type ClaimResponse struct {
	Claim         string          `json:"claim"`
	Task          TaskListEntry   `json:"task"`
	ResolvedImage *ImageReference `json:"resolved_image"`
	ImageSource   *string         `json:"image_source"`
	ClaimedAt     *string         `json:"claimed_at"`
	EnvironmentID *string         `json:"environment_id"`
	ScopeToken    *string         `json:"scope_token"`
}
