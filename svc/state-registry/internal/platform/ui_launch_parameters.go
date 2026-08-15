package platform

import "time"

const (
	LaunchParameterScopeGlobal   = "global"
	LaunchParameterScopeTeam     = "team"
	LaunchParameterScopeTaskType = "task_type"
)

type LaunchParameterWrite struct {
	Scope      string            `json:"scope"`
	TaskTypeID *string           `json:"task_type_id"`
	Name       string            `json:"name"`
	Env        map[string]string `json:"env"`
	Image      *string           `json:"image"`
}

type LaunchParameterDefinition struct {
	EnvironmentID string `json:"environment_id"`
	TeamID        string `json:"team_id"`
	Revision      int    `json:"revision"`
	LaunchParameterWrite
	Secrets   []LogicalSecretSummary `json:"secrets"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

type LaunchParameterRevision struct {
	EnvironmentID string            `json:"environment_id"`
	Revision      int               `json:"revision"`
	TeamID        string            `json:"team_id"`
	Scope         string            `json:"scope"`
	TaskTypeID    *string           `json:"task_type_id"`
	Name          string            `json:"name"`
	Env           map[string]string `json:"env"`
	SecretKeys    []string          `json:"secret_keys"`
	Image         *string           `json:"image"`
	Deleted       bool              `json:"deleted"`
	ActorID       string            `json:"actor_id"`
	RequestID     string            `json:"request_id"`
	CreatedAt     time.Time         `json:"created_at"`
}

type LaunchParameterPage struct {
	Items      []LaunchParameterDefinition `json:"items"`
	NextCursor *string                     `json:"next_cursor"`
}

type LaunchParameterRevisionPage struct {
	Items      []LaunchParameterRevision `json:"items"`
	NextCursor *string                   `json:"next_cursor"`
}

type UISecret struct {
	SecretID       string `json:"secret_id"`
	Key            string `json:"key"`
	CurrentVersion int    `json:"current_version"`
	Revoked        bool   `json:"revoked"`
}

type UISecretVersion struct {
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
}

type UISecretVersionPage struct {
	Items      []UISecretVersion `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}
