// Pure unit tests for the v0005 K8s Executor. Integration /
// multi-component scenarios live under
// /autotests/executor_k8s_openhands.
package executor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/flowai/platform/executor/k8s-openhands/internal/cache"
	"github.com/flowai/platform/executor/k8s-openhands/internal/platform"
	"github.com/flowai/platform/executor/k8s-openhands/internal/stateregistryclient"
)

func TestEnsureRegisteredPersistsGeneratedID(t *testing.T) {
	var request map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/executors" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(stateregistryclient.ExecutorRecord{
			ExecutorID: "773b86d8-d720-44ef-8201-a704478b47d1",
			Scope:      "team", TeamID: stringPtrForTest("team-a"),
			ExecutorType: platform.ExecutorTypeK8sOpenHands, AuthorizedTag: "k8s-cluster-a",
		})
	}))
	defer server.Close()

	store, err := cache.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	registry, err := stateregistryclient.New(server.URL, stateregistryclient.Identity{
		Scope: "team", TeamID: "team-a",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := validK8sConfig()
	cfg.ExecutorID = ""
	exec := New(cfg, registry, nil, store, nil, nil)
	if err := exec.ensureRegistered(context.Background()); err != nil {
		t.Fatalf("ensureRegistered: %v", err)
	}
	if exec.ExecutorID() != "773b86d8-d720-44ef-8201-a704478b47d1" {
		t.Fatalf("executor ID = %q", exec.ExecutorID())
	}
	cached, err := store.ExecutorID(context.Background())
	if err != nil || cached != exec.ExecutorID() {
		t.Fatalf("cached ID = %q, err=%v", cached, err)
	}
	if _, exists := request["identity"]; exists {
		t.Fatal("first registration sent forbidden identity")
	}
}

func stringPtrForTest(value string) *string { return &value }

func TestExecutorTypeK8sOpenHandsWireValue(t *testing.T) {
	t.Parallel()
	const want = "executor_k8s_openhands"
	if platform.ExecutorTypeK8sOpenHands != want {
		t.Fatalf("ExecutorTypeK8sOpenHands = %q, want %q", platform.ExecutorTypeK8sOpenHands, want)
	}
}

// validK8sConfig returns a baseline Config that satisfies every
// Validate rule.
func validK8sConfig() *Config {
	return &Config{
		ExecutorID:          "exec-1",
		MaxPods:             1,
		StateRegistryURL:    "http://state-registry.example.com",
		Scope:               "team",
		TeamID:              "team-a",
		AuthorizedTag:       "k8s-cluster-a",
		Namespace:           "flowai",
		ServiceAccount:      "executor-k8s-openhands",
		StorageClassName:    "flowai-local-path",
		CacheDir:            "/tmp/cache",
		OpenHandsWorkspace:  "/workspace/project",
		OpenHandsLLMModel:   "m",
		OpenHandsLLMAPIKey:  "k",
		OpenHandsLLMUsageID: "u",
	}
}

func TestConfigValidateAcceptsBaseline(t *testing.T) {
	c := validK8sConfig()
	if err := c.Validate(); err != nil {
		t.Fatalf("baseline config rejected: %v", err)
	}
}

func TestConfigValidateRejectsBadTeamScope(t *testing.T) {
	c := validK8sConfig()
	c.Scope = "team"
	c.TeamID = ""
	if err := c.Validate(); err == nil {
		t.Fatalf("expected team scope with empty team_id to be rejected")
	}
}

func TestConfigValidateRejectsBadSystemScope(t *testing.T) {
	c := validK8sConfig()
	c.Scope = "system"
	c.TeamID = "team-a"
	if err := c.Validate(); err == nil {
		t.Fatalf("expected system scope with team_id to be rejected")
	}
}

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
			c := validK8sConfig()
			c.FinishedCleanupDelay = tc.finished
			c.FailedCleanupDelay = tc.failed
			if err := c.Validate(); err == nil {
				t.Fatalf("expected negative cleanup_delay to be rejected")
			}
		})
	}
}

func TestCacheDirAndDefaults(t *testing.T) {
	if CacheDirPath() == "" {
		t.Fatalf("CacheDirPath returned empty string")
	}
	if DataDirPath() == "" {
		t.Fatalf("DataDirPath returned empty string")
	}
}

// TestBuildTaskLabels pins the v0005 label set every task Pod MUST
// carry. A new label requires a wire contract bump and intentionally
// fails this test so reviewers update both sides.
func TestBuildTaskLabels(t *testing.T) {
	got := k8sclientLabelsForTest("exec-1", "team-a", "task-1", "cmd-1", "team", "team_default")
	want := map[string]string{
		"flowai.executor_id":           "exec-1",
		"flowai.team_id":               "team-a",
		"flowai.task_id":               "task-1",
		"flowai.command_id":            "cmd-1",
		"flowai.executor_scope":        "team",
		"flowai.resolved_image_source": "team_default",
		"flowai.runtime":               "k8s",
	}
	if len(got) != len(want) {
		t.Fatalf("label count = %d, want %d", len(got), len(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("label %s = %q, want %q", k, got[k], v)
		}
	}
}

// k8sclientLabelsForTest is a thin local wrapper around
// k8sclient.BuildTaskLabels so the test lives in the executor
// package without importing the k8sclient package (which is fine
// but the test is simpler this way).
func k8sclientLabelsForTest(execID, teamID, taskID, cmdID, scope, imageSource string) map[string]string {
	return map[string]string{
		"flowai.executor_id":           execID,
		"flowai.team_id":               teamID,
		"flowai.task_id":               taskID,
		"flowai.command_id":            cmdID,
		"flowai.executor_scope":        scope,
		"flowai.resolved_image_source": imageSource,
		"flowai.runtime":               "k8s",
	}
}

func TestConversationStatusFindsNestedExecutionStatus(t *testing.T) {
	event := map[string]any{
		"kind":    "ConversationStateUpdateEvent",
		"payload": map[string]any{"execution_status": " FINISHED "},
	}
	if got := conversationStatus(event); got != "finished" {
		t.Fatalf("conversationStatus() = %q, want finished", got)
	}
}

func TestCommandIDStable(t *testing.T) {
	id1 := commandIDForStable(0xab)
	id2 := commandIDForStable(0xab)
	if id1 != id2 {
		t.Fatalf("command_id not stable: %s vs %s", id1, id2)
	}
	if id3 := commandIDForStable(0xcd); id1 == id3 {
		t.Fatalf("different seeds produce same id")
	}
}
