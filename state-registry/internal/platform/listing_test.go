package platform

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// TestTaskListEntryOpenAPISchema pins the wire field set of the
// canonical task row to every required key declared by the OpenAPI
// Task schema. A missing or extra key here is a contract violation.
func TestTaskListEntryOpenAPISchema(t *testing.T) {
	entry := TaskListEntry{
		TaskID:         "task-1",
		TeamID:         "team-a",
		SourceSystemID: "src-a",
		SourceID:       "src-1",
		TaskTypeID:     "tt-a",
		RequiredTag:    "tag-a",
		Payload:        json.RawMessage(`{"k":"v"}`),
		CurrentState:   TaskStatePending,
		IngestedAt:     "2024-01-01T00:00:00Z",
	}

	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	required := []string{
		"task_id",
		"team_id",
		"source_system_id",
		"source_id",
		"task_type_id",
		"required_tag",
		"payload",
		"current_state",
		"owner_command_id",
		"executor_id",
		"project_id",
		"environment_id",
		"image",
		"resolved_image",
		"image_source",
		"ingested_at",
		"claimed_at",
	}
	for _, key := range required {
		if _, ok := body[key]; !ok {
			t.Errorf("missing required Task field %q; body=%s", key, encoded)
		}
	}

	// Closed additionalProperties contract: every marshalled key
	// must be in the required list above.
	gotKeys := make([]string, 0, len(body))
	for key := range body {
		gotKeys = append(gotKeys, key)
	}
	sort.Strings(gotKeys)
	wantKeys := append([]string(nil), required...)
	sort.Strings(wantKeys)
	if strings.Join(gotKeys, ",") != strings.Join(wantKeys, ",") {
		t.Errorf("Task item keys drift: got=%v want=%v", gotKeys, wantKeys)
	}
}

// TestTaskListEntryNullablesRendersNull proves the nullable fields
// (project_id, environment_id, image, resolved_image, image_source,
// claimed_at, payload) marshal to JSON `null` when unset, matching
// the OpenAPI `anyOf: [<type>, "null"]` shape.
func TestTaskListEntryNullablesRendersNull(t *testing.T) {
	entry := TaskListEntry{
		TaskID:       "task-1",
		TeamID:       "team-a",
		CurrentState: TaskStatePending,
		IngestedAt:   "2024-01-01T00:00:00Z",
		Payload:      []byte("null"),
	}

	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	nullable := []string{
		"owner_command_id",
		"executor_id",
		"project_id",
		"environment_id",
		"image",
		"resolved_image",
		"image_source",
		"claimed_at",
	}
	for _, key := range nullable {
		raw, ok := body[key]
		if !ok {
			t.Errorf("missing nullable field %q", key)
			continue
		}
		if string(raw) != "null" {
			t.Errorf("nullable field %q rendered as %s, want null", key, raw)
		}
	}

	if got := string(body["payload"]); got != "null" {
		t.Errorf("payload rendered as %s, want null", got)
	}
}

// TestAdminTaskEntryAllowlistExact proves the admin projection is the
// documented exact 11-field allowlist and rejects any drift.
func TestAdminTaskEntryAllowlistExact(t *testing.T) {
	owner := "cmd-a"
	exec := "exec-a"
	claimed := "2024-01-01T01:02:03Z"
	entry := AdminTaskEntry{
		TaskID:         "task-1",
		TeamID:         "team-a",
		TaskTypeID:     "tt-a",
		SourceSystemID: "src-a",
		SourceID:       "src-1",
		RequiredTag:    "tag-a",
		CurrentState:   TaskStatePending,
		OwnerCommandID: &owner,
		ExecutorID:     &exec,
		IngestedAt:     "2024-01-01T00:00:00Z",
		ClaimedAt:      &claimed,
	}

	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	want := []string{
		"task_id",
		"team_id",
		"task_type_id",
		"source_system_id",
		"source_id",
		"required_tag",
		"current_state",
		"owner_command_id",
		"executor_id",
		"ingested_at",
		"claimed_at",
	}
	if got := len(body); got != len(want) {
		keys := make([]string, 0, len(body))
		for k := range body {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Fatalf("admin projection field count=%d, want %d; keys=%v body=%s", got, len(want), keys, encoded)
	}
	for _, key := range want {
		if _, ok := body[key]; !ok {
			t.Errorf("admin projection missing %q; body=%s", key, encoded)
		}
	}
}

// TestTaskListEntryToAdminTaskEntryRoundTrip proves the canonical
// row narrows onto the admin allowlist with every documented field
// preserved.
func TestTaskListEntryToAdminTaskEntryRoundTrip(t *testing.T) {
	owner := "cmd-a"
	exec := "exec-a"
	claimed := "2024-01-01T01:02:03Z"
	canonical := TaskListEntry{
		TaskID:         "task-1",
		TeamID:         "team-a",
		SourceSystemID: "src-a",
		SourceID:       "src-1",
		TaskTypeID:     "tt-a",
		RequiredTag:    "tag-a",
		Payload:        json.RawMessage(`{"k":"v"}`),
		CurrentState:   TaskStateRunning,
		OwnerCommandID: &owner,
		ExecutorID:     &exec,
		IngestedAt:     "2024-01-01T00:00:00Z",
		ClaimedAt:      &claimed,
	}

	admin := canonical.ToAdminTaskEntry()
	if admin.TaskID != canonical.TaskID {
		t.Errorf("TaskID=%q want %q", admin.TaskID, canonical.TaskID)
	}
	if admin.TeamID != canonical.TeamID {
		t.Errorf("TeamID=%q want %q", admin.TeamID, canonical.TeamID)
	}
	if admin.TaskTypeID != canonical.TaskTypeID {
		t.Errorf("TaskTypeID=%q want %q", admin.TaskTypeID, canonical.TaskTypeID)
	}
	if admin.SourceSystemID != canonical.SourceSystemID {
		t.Errorf("SourceSystemID=%q want %q", admin.SourceSystemID, canonical.SourceSystemID)
	}
	if admin.SourceID != canonical.SourceID {
		t.Errorf("SourceID=%q want %q", admin.SourceID, canonical.SourceID)
	}
	if admin.RequiredTag != canonical.RequiredTag {
		t.Errorf("RequiredTag=%q want %q", admin.RequiredTag, canonical.RequiredTag)
	}
	if admin.CurrentState != canonical.CurrentState {
		t.Errorf("CurrentState=%q want %q", admin.CurrentState, canonical.CurrentState)
	}
	if admin.OwnerCommandID == nil || *admin.OwnerCommandID != owner {
		t.Errorf("OwnerCommandID not preserved: %+v", admin.OwnerCommandID)
	}
	if admin.ExecutorID == nil || *admin.ExecutorID != exec {
		t.Errorf("ExecutorID not preserved: %+v", admin.ExecutorID)
	}
	if admin.IngestedAt != canonical.IngestedAt {
		t.Errorf("IngestedAt=%q want %q", admin.IngestedAt, canonical.IngestedAt)
	}
	if admin.ClaimedAt == nil || *admin.ClaimedAt != claimed {
		t.Errorf("ClaimedAt not preserved: %+v", admin.ClaimedAt)
	}
}

// TestGatewayTaskFilterDoesNotExposeTaskTypeID proves the documented
// Gateway filter surface intentionally omits task_type_id so a
// listener / operator cannot silently narrow the queue by task type
// outside the documented OpenAPI TaskPage contract.
func TestGatewayTaskFilterDoesNotExposeTaskTypeID(t *testing.T) {
	encoded, err := json.Marshal(GatewayTaskFilter{State: TaskStatePending, Tag: "tag-a"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "task_type_id") {
		t.Errorf("GatewayTaskFilter serialized as %s; expected no task_type_id field", encoded)
	}
}
