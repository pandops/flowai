// Pure unit tests for the executor package. Integration / multi-component
// scenarios live under /autotests/executor_docker_openhands.
package executor

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/flowai/platform/executor/docker_openhands/internal/platform"
)

// setRequiredStateRegistryEnv sets every State Registry env var
// the operator must configure for the v0009 contract. The
// EXECUTOR_STATE_REGISTRY_TLS_* env vars are accepted-but-ignored
// legacy keys; they are set here to assert that the operator can
// keep them during staged configuration cleanup.
func setRequiredStateRegistryEnv(t *testing.T) {
	t.Helper()
	t.Setenv("EXECUTOR_STATE_REGISTRY_URL", "http://state-registry.example.com")
	t.Setenv("EXECUTOR_SCOPE", "team")
	t.Setenv("EXECUTOR_TEAM_ID", "team-a")
	t.Setenv("EXECUTOR_AUTHORIZED_TAG", "openhands")
	t.Setenv("EXECUTOR_STATE_REGISTRY_TLS_CLIENT_CERT", "/tmp/client.crt")
	t.Setenv("EXECUTOR_STATE_REGISTRY_TLS_CLIENT_KEY", "/tmp/client.key")
	t.Setenv("EXECUTOR_STATE_REGISTRY_TLS_SERVER_CA", "/tmp/server-ca.crt")
}

func TestConfigValidateRejectsBadRange(t *testing.T) {
	cfg := &Config{
		ExecutorID:          "exec-1",
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

func TestConfigValidateRejectsMissingV1Agent(t *testing.T) {
	cfg := &Config{
		ExecutorID:         "exec-1",
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
	setRequiredStateRegistryEnv(t)
	tmp := t.TempDir()
	path := tmp + "/cfg.yaml"
	yaml := `executor_max_containers: 4
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
	if cfg.MaxContainers != 4 {
		t.Fatalf("max_containers: %d", cfg.MaxContainers)
	}
	if cfg.OpenHandsPortStart != 20000 {
		t.Fatalf("port start: %d", cfg.OpenHandsPortStart)
	}
}

func TestConfigLoadLeavesExecutorIDForRegistry(t *testing.T) {
	setRequiredStateRegistryEnv(t)
	t.Setenv("OPENHANDS_AGENT_PROFILE_ID", "flowai-default")
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ExecutorID != "" {
		t.Fatalf("executor_id = %q, want Registry-generated empty first-start value", cfg.ExecutorID)
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
	want := "executor_docker_openhands"
	if platform.ExecutorTypeDockerOpenHands != want {
		t.Fatalf("ExecutorTypeDockerOpenHands = %q, want %q",
			platform.ExecutorTypeDockerOpenHands, want)
	}
	if !strings.HasPrefix(platform.ExecutorTypeDockerOpenHands, "executor_") {
		t.Fatalf("wire value %q must start with executor_", platform.ExecutorTypeDockerOpenHands)
	}
}

func TestLoadConfigGeneratesOpenHandsAPIKey(t *testing.T) {
	setRequiredStateRegistryEnv(t)
	t.Setenv("OPENHANDS_AGENT_PROFILE_ID", "flowai-default")
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.OpenHandsAPIKey == "" {
		t.Fatalf("expected OpenHandsAPIKey to be auto-generated when none configured")
	}
}

func TestLoadConfigPreservesExplicitOpenHandsAPIKey(t *testing.T) {
	setRequiredStateRegistryEnv(t)
	t.Setenv("OPENHANDS_AGENT_PROFILE_ID", "flowai-default")
	t.Setenv("OPENHANDS_API_KEY", "operator-supplied-key")
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.OpenHandsAPIKey != "operator-supplied-key" {
		t.Fatalf("expected OpenHandsAPIKey = %q, got %q",
			"operator-supplied-key", cfg.OpenHandsAPIKey)
	}
}

// validStateRegistryConfig returns a baseline cfg that satisfies
// every Validate rule. The legacy State Registry TLS material
// fields are accepted but never read after v0009; tests that want
// to assert missing-field handling should not be tied to those
// fields.
func validStateRegistryConfig() *Config {
	return &Config{
		ExecutorID:           "exec-1",
		MaxContainers:        1,
		OpenHandsPortStart:   19000,
		OpenHandsPortEnd:     19001,
		OpenHandsWorkspace:   "/workspace/project",
		OpenHandsLLMModel:    "m",
		OpenHandsLLMAPIKey:   "k",
		OpenHandsLLMUsageID:  "u",
		OpenHandsStartupTO:   30 * time.Second,
		OpenHandsDrainTO:     30 * time.Second,
		WebSocketDialTimeout: 5 * time.Second,
		StateRegistryURL:     "http://state-registry.example.com",
		Scope:                "team",
		TeamID:               "team-a",
		AuthorizedTag:        "openhands",
	}
}

// TestConfigValidateAcceptsPlainHTTP asserts the v0009 contract:
// the State Registry transport is plaintext (http://) and the
// https-only / mTLS-material requirements are removed.
func TestConfigValidateAcceptsPlainHTTP(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"http scheme", "http://state-registry.example.com"},
		{"loopback http", "http://127.0.0.1:9443"},
		{"https still valid", "https://state-registry.example.com:9443"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validStateRegistryConfig()
			cfg.StateRegistryURL = tc.url
			if err := cfg.Validate(); err != nil {
				t.Fatalf("expected http(s) URL to validate, got %v", err)
			}
		})
	}
}

// TestConfigValidateIgnoresLegacyTLSTriple asserts the v0009
// contract: the legacy EXECUTOR_STATE_REGISTRY_TLS_* env vars
// are accepted but never read or validated. A baseline config
// validates without them.
func TestConfigValidateIgnoresLegacyTLSTriple(t *testing.T) {
	cfg := validStateRegistryConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("baseline config without legacy TLS material rejected: %v", err)
	}
}

func TestConfigValidateRequiresStateRegistryURL(t *testing.T) {
	cfg := validStateRegistryConfig()
	cfg.StateRegistryURL = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected missing State Registry URL to be rejected")
	}
}

func TestDockerEnvWithSessionAPIKey(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
		key    string
		want   map[string]string
	}{
		{
			name: "override caller-provided with configured key",
			values: map[string]string{
				"SESSION_API_KEY": "caller-supplied",
				"FOO":             "bar",
			},
			key: "configured-key",
			want: map[string]string{
				"SESSION_API_KEY": "configured-key",
				"FOO":             "bar",
			},
		},
		{
			name: "inject key when caller did not provide one",
			values: map[string]string{
				"OTHER": "value",
			},
			key: "configured-key",
			want: map[string]string{
				"SESSION_API_KEY": "configured-key",
				"OTHER":           "value",
			},
		},
		{
			name: "strip caller-provided when configured key is empty",
			values: map[string]string{
				"SESSION_API_KEY": "caller-supplied",
				"FOO":             "bar",
			},
			key: "",
			want: map[string]string{
				"FOO": "bar",
			},
		},
		{
			name: "empty key and absent caller value keeps SESSION_API_KEY absent",
			values: map[string]string{
				"FOO": "bar",
			},
			key: "",
			want: map[string]string{
				"FOO": "bar",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dockerEnvWithSessionAPIKey(tt.values, tt.key)
			parsed := map[string]string{}
			for _, entry := range got {
				eq := strings.IndexByte(entry, '=')
				if eq < 0 {
					t.Fatalf("entry %q missing '=' separator", entry)
				}
				parsed[entry[:eq]] = entry[eq+1:]
			}
			if len(parsed) != len(tt.want) {
				t.Fatalf("entry count = %d, want %d (entries=%v)",
					len(parsed), len(tt.want), parsed)
			}
			for k, v := range tt.want {
				if parsed[k] != v {
					t.Fatalf("entry %q = %q, want %q", k, parsed[k], v)
				}
			}
		})
	}
}

func TestDockerEnvWithSessionAPIKeyDoesNotMutateInput(t *testing.T) {
	values := map[string]string{
		"SESSION_API_KEY": "caller-supplied",
		"FOO":             "bar",
	}
	snapshot := map[string]string{
		"SESSION_API_KEY": "caller-supplied",
		"FOO":             "bar",
	}
	_ = dockerEnvWithSessionAPIKey(values, "configured-key")
	if len(values) != len(snapshot) {
		t.Fatalf("input size changed: got %d, want %d", len(values), len(snapshot))
	}
	for k, want := range snapshot {
		if got := values[k]; got != want {
			t.Fatalf("input mutated at %q: got %q, want %q", k, got, want)
		}
	}
}

// TestConfigValidateRejectsNegativeCleanupDelay asserts the v0005
// contract: cleanup delays are non-negative.
func TestConfigValidateRejectsNegativeCleanupDelay(t *testing.T) {
	cases := []struct {
		name     string
		finished time.Duration
		failed   time.Duration
	}{
		{"negative finished", -1 * time.Second, 0},
		{"negative failed", 0, -1 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validStateRegistryConfig()
			cfg.FinishedCleanupDelay = tc.finished
			cfg.FailedCleanupDelay = tc.failed
			if err := cfg.Validate(); err == nil {
				t.Fatalf("expected negative cleanup_delay to be rejected")
			}
		})
	}
}

// TestConfigLoadHonoursCleanupDelayDefaults asserts the YAML+env
// path applies the documented defaults (each 0s) when unset.
func TestConfigLoadHonoursCleanupDelayDefaults(t *testing.T) {
	setRequiredStateRegistryEnv(t)
	t.Setenv("OPENHANDS_AGENT_PROFILE_ID", "flowai-default")
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.FinishedCleanupDelay != 0 {
		t.Fatalf("FinishedCleanupDelay default = %s, want 0s", cfg.FinishedCleanupDelay)
	}
	if cfg.FailedCleanupDelay != 0 {
		t.Fatalf("FailedCleanupDelay default = %s, want 0s", cfg.FailedCleanupDelay)
	}
}

// TestConfigLoadHonoursCleanupDelayEnv asserts the YAML+env path
// applies the v0005 env overrides for cleanup delays.
func TestConfigLoadHonoursCleanupDelayEnv(t *testing.T) {
	setRequiredStateRegistryEnv(t)
	t.Setenv("OPENHANDS_AGENT_PROFILE_ID", "flowai-default")
	t.Setenv("EXECUTOR_FINISHED_CLEANUP_DELAY", "30s")
	t.Setenv("EXECUTOR_FAILED_CLEANUP_DELAY", "5s")
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.FinishedCleanupDelay != 30*time.Second {
		t.Fatalf("FinishedCleanupDelay env = %s, want 30s", cfg.FinishedCleanupDelay)
	}
	if cfg.FailedCleanupDelay != 5*time.Second {
		t.Fatalf("FailedCleanupDelay env = %s, want 5s", cfg.FailedCleanupDelay)
	}
}

// TestSlotAcceptedTerminalRecord pins the slot's accepted-terminal
// record so review can match v0005 cleanup-delay selection by event
// type without touching real Docker.
func TestSlotAcceptedTerminalRecord(t *testing.T) {
	slot := &taskSlot{}
	if event, at := slot.acceptedTerminalRecord(); event != "" || !at.IsZero() {
		t.Fatalf("expected no accepted terminal on a fresh slot, got event=%q at=%s", event, at)
	}
	slot.recordAcceptedTerminal(platform.TaskEventTypeFinished)
	event, at := slot.acceptedTerminalRecord()
	if event != platform.TaskEventTypeFinished {
		t.Fatalf("event = %q, want %q", event, platform.TaskEventTypeFinished)
	}
	if at.IsZero() {
		t.Fatalf("accepted_at should be populated after recordAcceptedTerminal")
	}
}
