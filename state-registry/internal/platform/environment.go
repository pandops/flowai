package platform

import "time"

type EnvironmentScope struct {
	ProjectID    *string `json:"project_id"`
	TaskID       *string `json:"task_id"`
	ParentTaskID *string `json:"parent_task_id"`
}

type EnvironmentWriteRequest struct {
	Name   string            `json:"name"`
	Scope  EnvironmentScope  `json:"scope"`
	Values map[string]string `json:"values"`
}

type LogicalSecretSummary struct {
	SecretID      string `json:"secret_id"`
	TeamID        string `json:"team_id"`
	EnvironmentID string `json:"environment_id"`
	Name          string `json:"name"`
	LatestVersion int    `json:"latest_version"`
	Revoked       bool   `json:"revoked"`
}

type Environment struct {
	EnvironmentID string                 `json:"environment_id"`
	TeamID        string                 `json:"team_id"`
	Revision      int                    `json:"revision"`
	Name          string                 `json:"name"`
	Scope         EnvironmentScope       `json:"scope"`
	Values        map[string]string      `json:"values"`
	Secrets       []LogicalSecretSummary `json:"secrets"`
	Deleted       bool                   `json:"deleted"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
}

type EnvironmentPage struct {
	Items []Environment `json:"items"`
	Page  AdminPageInfo `json:"page"`
}

type OpenEnvironmentResponse struct {
	TeamID        string            `json:"team_id"`
	ProjectID     *string           `json:"project_id"`
	TaskID        string            `json:"task_id"`
	EnvironmentID string            `json:"environment_id"`
	ExecutorID    string            `json:"executor_id"`
	Values        map[string]string `json:"values"`
}
