// Package fixtures holds reusable test fixtures for the executor-docker service.
//
// The task_spec fixture is a sample Router task that can be POSTed to the
// mocked task server's /v1/tasks seed via SetRouterTask, then observed as
// it flows through the executor. It matches the platform.RouterTask wire
// shape documented in the OpenAPI contract.
package fixtures

import (
	"github.com/google/uuid"

	"github.com/flowai/platform/executor-docker/internal/platform"
)

// TaskSpec returns a sample queued task suitable for integration verification.
// The prompt is intentionally minimal and deterministic so the integration
// tests can assert behavior without depending on agent runtime output.
func TaskSpec() *platform.RouterTask {
	return &platform.RouterTask{
		TaskID:        uuid.NewString(),
		RoutingTarget: "openhands",
		AgentRuntime:  "openhands",
		Status:        platform.TaskStatusQueued,
		Prompt:        "echo integration-verification-ok",
		Metadata: map[string]interface{}{
			"scenario":  "integration-verification",
			"v0001":     true,
			"submitted": "task_spec_fixture",
		},
	}
}

// TaskSpecWithID returns a TaskSpec fixture with the given task id, useful
// when a test wants to track the same task across multiple assertions.
func TaskSpecWithID(taskID string) *platform.RouterTask {
	t := TaskSpec()
	t.TaskID = taskID
	return t
}