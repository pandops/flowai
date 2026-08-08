package platform

import "testing"

func TestExecutorTypeDockerOpenHandsWireValue(t *testing.T) {
	t.Parallel()

	const want = "executor_docker_openhands"
	if ExecutorTypeDockerOpenHands != want {
		t.Fatalf("ExecutorTypeDockerOpenHands = %q, want %q", ExecutorTypeDockerOpenHands, want)
	}
}
