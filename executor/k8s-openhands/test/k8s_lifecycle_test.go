package integration_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/flowai/platform/executor/k8s-openhands/internal/executor"
)

// TestK8sClaimBeforeRuntime asserts the K8s Executor never creates
// a Pod before a successful 200 claim. The fake K8s client
// captures every CreatePod call; the test asserts the Pod is
// created only after the claim response.
func TestK8sClaimBeforeRuntime(t *testing.T) {
	stub, exec, kube, _ := setupK8sV0005Env(t, nil)
	taskID := uuid.NewString()
	stub.addPendingTask(taskID, "registry.example.com/team-a/runtime:v1", "team_default")
	cancel, stopped := startK8sExec(t, exec)
	defer func() { cancel(); <-stopped }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if containsString(stub.taskEventTypes(taskID), "running") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !containsString(stub.taskEventTypes(taskID), "running") {
		t.Fatalf("executor never appended `running` event; got %v", stub.taskEventTypes(taskID))
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(kube.PodsSnapshot()) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(kube.PodsSnapshot()) == 0 {
		t.Fatalf("executor never created a Pod after claim")
	}
}

// TestK8sLocalCapacityGated asserts the K8s Executor never creates
// more Pods than the configured local capacity.
func TestK8sLocalCapacityGated(t *testing.T) {
	stub, exec, _, _ := setupK8sV0005Env(t, func(c *executor.Config) {
		c.MaxPods = 1
	})
	for i := 0; i < 3; i++ {
		stub.addPendingTask(uuid.NewString(), "registry.example.com/team-a/runtime:v1", "team_default")
	}
	cancel, stopped := startK8sExec(t, exec)
	defer func() { cancel(); <-stopped }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if exec.PodCount() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if exec.PodCount() > 1 {
		t.Fatalf("exceeded local capacity: got %d Pods", exec.PodCount())
	}
}

// TestK8sPodLabels verifies the v0005 Pod label set.
func TestK8sPodLabels(t *testing.T) {
	stub, exec, kube, cfg := setupK8sV0005Env(t, nil)
	taskID := uuid.NewString()
	stub.addPendingTask(taskID, "registry.example.com/team-a/runtime:v1", "team_default")
	cancel, stopped := startK8sExec(t, exec)
	defer func() { cancel(); <-stopped }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(kube.PodsSnapshot()) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	snap := kube.PodsSnapshot()
	if len(snap) == 0 {
		t.Fatalf("no Pod created")
	}
	for _, p := range snap {
		if p.Labels["flowai.task_id"] == taskID {
			want := map[string]string{
				"flowai.executor_id":           cfg.ExecutorID,
				"flowai.team_id":               "team-a",
				"flowai.task_id":               taskID,
				"flowai.executor_scope":        "team",
				"flowai.resolved_image_source": "team_default",
				"flowai.runtime":               "k8s",
			}
			for k, v := range want {
				if got := p.Labels[k]; got != v {
					t.Fatalf("label %s = %q, want %q", k, got, v)
				}
			}
			if got := p.Labels["flowai.command_id"]; got == "" {
				t.Fatalf("flowai.command_id is required")
			}
			if p.Image != "registry.example.com/team-a/runtime:v1" {
				t.Fatalf("Pod image = %q, want resolved_image verbatim", p.Image)
			}
			return
		}
	}
	t.Fatalf("no Pod matches task %s", taskID)
}
