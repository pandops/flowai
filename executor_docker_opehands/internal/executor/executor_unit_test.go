// Pure unit tests for the executor package. Integration / multi-component
// scenarios live under /autotests/executor_docker_opehands.
package executor

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/flowai/platform/executor_docker_opehands/internal/platform"
)

func setRequiredStateRegistryEnv(t *testing.T) {
	t.Helper()
	t.Setenv("EXECUTOR_STATE_REGISTRY_URL", "https://state-registry.example.com")
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

func TestConfigLoadGeneratesExecutorID(t *testing.T) {
	setRequiredStateRegistryEnv(t)
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

// validStateRegistryConfig returns a baseline cfg that satisfies every
// pre-existing Validate rule plus a complete mTLS material triple. Tests
// that want to drop a single piece of the triple call the returned cfg
// mutator to clear it and then assert Validate fails closed.
func validStateRegistryConfig() *Config {
	return &Config{
		ExecutorID:                   "exec-1",
		MaxContainers:                1,
		OpenHandsPortStart:           19000,
		OpenHandsPortEnd:             19001,
		OpenHandsWorkspace:           "/workspace/project",
		OpenHandsLLMModel:            "m",
		OpenHandsLLMAPIKey:           "k",
		OpenHandsLLMUsageID:          "u",
		OpenHandsStartupTO:           30 * time.Second,
		OpenHandsDrainTO:             30 * time.Second,
		WebSocketDialTimeout:         5 * time.Second,
		StateRegistryURL:             "https://state-registry.example.com",
		Scope:                        "team",
		TeamID:                       "team-a",
		AuthorizedTag:                "openhands",
		StateRegistryTLSCertPath:     "/etc/flowai/client.crt",
		StateRegistryTLSKeyPath:      "/etc/flowai/client.key",
		StateRegistryTLSServerCAPath: "/etc/flowai/server-ca.crt",
	}
}

// TestConfigValidateRequiresHTTPSWhenStateRegistryConfigured asserts
// that a non-empty StateRegistryURL forces a https URL. Production
// connect to the State Registry is mTLS and mTLS requires TLS.
func TestConfigValidateRequiresHTTPSWhenStateRegistryConfigured(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"http scheme", "http://state-registry.example.com"},
		{"loopback http", "http://127.0.0.1:9443"},
		{"http with port", "http://state-registry.example.com:9443"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validStateRegistryConfig()
			cfg.StateRegistryURL = tc.url
			if err := cfg.Validate(); err == nil {
				t.Fatalf("expected https-only validation error for %q", tc.url)
			}
		})
	}
}

// TestConfigValidateRequiresClientCertKeyAndServerCA asserts that
// when StateRegistryURL is set, all three TLS material paths are
// required: the operator-supplied client cert, the matching private
// key, and the server CA bundle that anchors the State Registry
// server certificate. Dropping any one of them must fail closed.
func TestConfigValidateRequiresClientCertKeyAndServerCA(t *testing.T) {
	base := validStateRegistryConfig()
	if err := base.Validate(); err != nil {
		t.Fatalf("baseline mTLS config rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"empty client cert", func(c *Config) { c.StateRegistryTLSCertPath = "" }},
		{"empty client key", func(c *Config) { c.StateRegistryTLSKeyPath = "" }},
		{"empty server CA", func(c *Config) { c.StateRegistryTLSServerCAPath = "" }},
		{"only cert present", func(c *Config) {
			c.StateRegistryTLSKeyPath = ""
			c.StateRegistryTLSServerCAPath = ""
		}},
		{"only key present", func(c *Config) {
			c.StateRegistryTLSCertPath = ""
			c.StateRegistryTLSServerCAPath = ""
		}},
		{"only server ca present", func(c *Config) {
			c.StateRegistryTLSCertPath = ""
			c.StateRegistryTLSKeyPath = ""
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validStateRegistryConfig()
			tc.mutate(cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("expected validate to reject incomplete mTLS material (%s)", tc.name)
			}
		})
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
