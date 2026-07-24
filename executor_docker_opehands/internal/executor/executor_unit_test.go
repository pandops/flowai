// Pure unit tests for the executor package. Integration / multi-component
// scenarios live under /autotests/executor_docker_opehands.
package executor

import (
	"os"
	"strings"
	"testing"

	"github.com/flowai/platform/executor_docker_opehands/internal/platform"
)

func TestConfigValidateRejectsBadRange(t *testing.T) {
	cfg := &Config{
		ExecutorID:          "exec-1",
		RoutingTarget:       "openhands",
		MaxContainers:       5,
		OpenHandsPortStart:  19000,
		OpenHandsPortEnd:    19001,
		OpenHandsWorkspace:  "/workspace/project",
		OpenHandsLLMModel:   "m",
		OpenHandsLLMAPIKey:  "k",
		OpenHandsLLMUsageID: "u",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error for too-small port range")
	}
}

func TestConfigValidateRejectsEmptyRoutingTarget(t *testing.T) {
	cfg := &Config{
		ExecutorID:          "exec-1",
		RoutingTarget:       "",
		MaxContainers:       1,
		OpenHandsPortStart:  19000,
		OpenHandsPortEnd:    19001,
		OpenHandsWorkspace:  "/workspace/project",
		OpenHandsLLMModel:   "m",
		OpenHandsLLMAPIKey:  "k",
		OpenHandsLLMUsageID: "u",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error for empty routing target")
	}
}

func TestConfigValidateRejectsMissingV1Agent(t *testing.T) {
	cfg := &Config{
		ExecutorID:         "exec-1",
		RoutingTarget:      "openhands",
		MaxContainers:      1,
		OpenHandsPortStart: 19000,
		OpenHandsPortEnd:   19000,
		OpenHandsWorkspace: "/workspace/project",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error for missing V1 agent profile/model")
	}
}

func TestConfigLoadFromYAML(t *testing.T) {
	tmp := t.TempDir()
	path := tmp + "/cfg.yaml"
	yaml := `routing_target: claude
executor_max_containers: 4
openhands_host_port_start: 20000
openhands_host_port_end: 20010
openhands_llm_model: openai/gpt-4o-mini
openhands_llm_api_key: test-key
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.RoutingTarget != "claude" {
		t.Fatalf("routing_target: %s", cfg.RoutingTarget)
	}
	if cfg.MaxContainers != 4 {
		t.Fatalf("max_containers: %d", cfg.MaxContainers)
	}
	if cfg.OpenHandsPortStart != 20000 {
		t.Fatalf("port start: %d", cfg.OpenHandsPortStart)
	}
}

func TestConfigLoadGeneratesExecutorID(t *testing.T) {
	t.Setenv("OPENHANDS_AGENT_PROFILE_ID", "flowai-default")
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ExecutorID == "" {
		t.Fatalf("expected executor_id to be auto-generated")
	}
	if len(cfg.ExecutorID) < 8 {
		t.Fatalf("expected executor_id to be reasonably long, got %q", cfg.ExecutorID)
	}
}

func TestStateStringValues(t *testing.T) {
	states := []State{StateStarting, StateRegistering, StateReady, StateBusy, StateStopping, StateStopped, StateFailed}
	for _, s := range states {
		if string(s) == "" {
			t.Fatalf("empty state value")
		}
	}
}

// TestExecutorTypeWireValue pins the canonical executor_type wire value
// to the concrete service directory name and the executor_<runtime>_<tool>
// convention. Renaming the wire value (or the directory) without updating
// the other will fail this test.
func TestExecutorTypeWireValue(t *testing.T) {
	want := "executor_docker_opehands"
	if platform.ExecutorTypeDockerOpenHands != want {
		t.Fatalf("ExecutorTypeDockerOpenHands = %q, want %q",
			platform.ExecutorTypeDockerOpenHands, want)
	}
	if !strings.HasPrefix(platform.ExecutorTypeDockerOpenHands, "executor_") {
		t.Fatalf("wire value %q must start with executor_", platform.ExecutorTypeDockerOpenHands)
	}
}
