// Pure unit tests for the executor package. Integration / multi-component
// scenarios live under /autotests/docker-executor.
package executor

import (
	"os"
	"testing"
)

func TestConfigValidateRejectsBadRange(t *testing.T) {
	cfg := &Config{
		ExecutorID:         "exec-1",
		RoutingTarget:      "openhands",
		MaxContainers:      5,
		OpenHandsPortStart: 19000,
		OpenHandsPortEnd:   19001,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error for too-small port range")
	}
}

func TestConfigValidateRejectsEmptyRoutingTarget(t *testing.T) {
	cfg := &Config{
		ExecutorID:         "exec-1",
		RoutingTarget:      "",
		MaxContainers:      1,
		OpenHandsPortStart: 19000,
		OpenHandsPortEnd:   19001,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error for empty routing target")
	}
}

func TestConfigLoadFromYAML(t *testing.T) {
	tmp := t.TempDir()
	path := tmp + "/cfg.yaml"
	yaml := `routing_target: claude
executor_max_containers: 4
openhands_host_port_start: 20000
openhands_host_port_end: 20010
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
	states := []State{StateStarting, StateRegistering, StateReady, StateBusy, StateDraining, StateStopped}
	for _, s := range states {
		if string(s) == "" {
			t.Fatalf("empty state value")
		}
	}
}