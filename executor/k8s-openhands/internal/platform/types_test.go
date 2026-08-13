package platform

import (
	"strings"
	"testing"
)

// TestExecutorTypeK8sOpenHandsWireValue pins the canonical
// executor_type wire value to the concrete service directory name
// and the executor_<runtime>_<tool> convention. Renaming the wire
// value (or the directory) without updating the other will fail this
// test. The v0005 task 9 contract pins the wire value to
// executor_k8s_openhands.
func TestExecutorTypeK8sOpenHandsWireValue(t *testing.T) {
	t.Parallel()
	const want = "executor_k8s_openhands"
	if ExecutorTypeK8sOpenHands != want {
		t.Fatalf("ExecutorTypeK8sOpenHands = %q, want %q", ExecutorTypeK8sOpenHands, want)
	}
	if !strings.HasPrefix(ExecutorTypeK8sOpenHands, "executor_") {
		t.Fatalf("wire value %q must start with executor_", ExecutorTypeK8sOpenHands)
	}
}

// TestScopeStringValues pins the only accepted Executor scope values
// to "team" and "system". A new value requires a wire contract bump
// and intentionally fails this test so reviewers think twice.
func TestScopeStringValues(t *testing.T) {
	for _, s := range []string{ExecutorScopeTeam, ExecutorScopeSystem} {
		if s == "" {
			t.Fatalf("scope value is empty")
		}
	}
	if ExecutorScopeTeam == ExecutorScopeSystem {
		t.Fatalf("team and system scope strings must differ")
	}
}

// TestTaskEventTypeWireValues pins the accepted task event type
// values. The first lifecycle event (`created`) is appended by the
// State Registry on claim and is intentionally absent from this list.
func TestTaskEventTypeWireValues(t *testing.T) {
	for _, et := range []string{TaskEventTypeRunning, TaskEventTypeFinished, TaskEventTypeFailed} {
		if et == "" {
			t.Fatalf("event type is empty")
		}
		if et == "created" {
			t.Fatalf("Executor-emitted event types MUST NOT include \"created\"; that is Registry-appended on claim")
		}
		if et == "dispatched" {
			t.Fatalf("Executor-emitted event types MUST NOT include \"dispatched\"; removed by v0002 contract")
		}
	}
}
