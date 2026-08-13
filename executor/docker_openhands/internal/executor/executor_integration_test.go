//go:build integration

package executor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/flowai/platform/executor/docker_openhands/internal/dockerclient"
	"github.com/flowai/platform/executor/docker_openhands/internal/platform"
)

func TestV0002TerminalCleanupDelayAgainstDocker(t *testing.T) {
	socket := dockerSocketPath(t)
	client, err := dockerclient.NewSDKClient(socket)
	if err != nil {
		t.Fatalf("new Docker client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		t.Skipf("Docker Engine unavailable: %v", err)
	}

	image := os.Getenv("FLOWAI_OPENHANDS_TEST_IMAGE")
	if image == "" {
		image = "localhost/agent-openhands-image:latest"
	}
	if err := client.PullImage(ctx, image, "never"); err != nil {
		t.Fatalf("test image unavailable: %v", err)
	}

	tests := []struct {
		name      string
		eventType string
		finished  time.Duration
		failed    time.Duration
	}{
		{name: "finished selects finished delay", eventType: platform.TaskEventTypeFinished, finished: 300 * time.Millisecond, failed: 2 * time.Second},
		{name: "failed selects failed delay", eventType: platform.TaskEventTypeFailed, finished: 2 * time.Second, failed: 300 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executorID := "integration-" + strings.NewReplacer(" ", "-", "_", "-").Replace(tt.name)
			ref, err := client.StartContainer(ctx, dockerclient.ContainerSpec{
				Name:    fmt.Sprintf("flowai-v0005-cleanup-%d", time.Now().UnixNano()),
				Image:   image,
				Command: []string{"sh", "-c", "sleep 300"},
				Labels:  map[string]string{dockerclient.LabelExecutorID: executorID},
			})
			if err != nil {
				t.Fatalf("start test container: %v", err)
			}
			t.Cleanup(func() { _ = client.ForceKill(context.Background(), ref.ID) })

			cfg := validStateRegistryConfig()
			cfg.FinishedCleanupDelay = tt.finished
			cfg.FailedCleanupDelay = tt.failed
			exec := New(cfg, client, slog.New(slog.NewTextHandler(os.Stderr, nil)))
			slot := &taskSlot{containerID: ref.ID, doneCh: make(chan struct{}), exitCh: make(chan struct{})}
			exec.addSlot("task-delay", slot)
			slot.recordAcceptedTerminal(tt.eventType)

			cleaned := make(chan struct{})
			started := time.Now()
			go func() {
				exec.cleanupContainer(slot)
				close(cleaned)
			}()

			time.Sleep(100 * time.Millisecond)
			if got := exec.RunningChildCount(); got != 1 {
				t.Fatalf("running count during cleanup delay = %d, want 1", got)
			}
			if !dockerContainerExists(t, ctx, client, executorID, ref.ID) {
				t.Fatal("container was removed before selected cleanup delay expired")
			}
			select {
			case <-cleaned:
			case <-time.After(5 * time.Second):
				t.Fatal("cleanup did not finish")
			}
			if elapsed := time.Since(started); elapsed < 250*time.Millisecond || elapsed >= time.Second {
				t.Fatalf("selected cleanup delay elapsed = %s, want about 300ms", elapsed)
			}
			if dockerContainerExists(t, ctx, client, executorID, ref.ID) {
				t.Fatal("container still exists after cleanup delay")
			}

			// A second cleanup is a no-op and must tolerate the already-removed container.
			exec.cleanupContainer(slot)
		})
	}
}

func dockerSocketPath(t *testing.T) string {
	t.Helper()
	host := os.Getenv("DOCKER_HOST")
	if strings.HasPrefix(host, "unix://") {
		return strings.TrimPrefix(host, "unix://")
	}
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		candidate := runtimeDir + "/podman/podman.sock"
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func dockerContainerExists(t *testing.T, ctx context.Context, client dockerclient.Client, executorID, containerID string) bool {
	t.Helper()
	refs, err := client.ListOwned(ctx, executorID)
	if err != nil {
		t.Fatalf("list Docker containers: %v", err)
	}
	for _, ref := range refs {
		if ref.ID == containerID {
			return true
		}
	}
	return false
}
