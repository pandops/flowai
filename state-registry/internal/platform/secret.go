package platform

import "time"

type SecretCreateRequest struct {
	Name  string           `json:"name"`
	Value string           `json:"value"`
	Scope EnvironmentScope `json:"scope"`
}

type SecretReplaceRequest struct {
	Value string `json:"value"`
}

type LogicalSecret struct {
	SecretID      string           `json:"secret_id"`
	TeamID        string           `json:"team_id"`
	EnvironmentID string           `json:"environment_id"`
	Name          string           `json:"name"`
	Scope         EnvironmentScope `json:"scope"`
	LatestVersion int              `json:"latest_version"`
	Revoked       bool             `json:"revoked"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
}

type SecretVersion struct {
	SecretID   string    `json:"secret_id"`
	TeamID     string    `json:"team_id"`
	Version    int       `json:"version"`
	KeyID      string    `json:"key_id"`
	KeyVersion int       `json:"key_version"`
	CreatedAt  time.Time `json:"created_at"`
}

type SecretWriteResponse struct {
	Secret  LogicalSecret `json:"secret"`
	Version SecretVersion `json:"version"`
}

type LogicalSecretPage struct {
	Items []LogicalSecret `json:"items"`
	Page  AdminPageInfo   `json:"page"`
}

type SecretVersionPage struct {
	Items []SecretVersion `json:"items"`
	Page  AdminPageInfo   `json:"page"`
}
