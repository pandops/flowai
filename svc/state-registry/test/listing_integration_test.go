//go:build integration

package test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
	"github.com/flowai/platform/svc/state-registry/internal/store"
)

// TestListTaskTypesOrdering proves the SQL projection is the
// documented 3-field allowlist and that the deterministic
// (team_id ASC, execution_tag ASC, task_type_id ASC) ordering is
// respected across the global collection.
func TestListTaskTypesOrdering(t *testing.T) {
	db := migratedDB(t)
	repo := store.New(db)
	ctx := context.Background()

	for _, team := range []struct {
		id, name string
	}{
		{"team-b", "Team B"},
		{"team-a", "Team A"},
	} {
		mustInsertAdminTeam(t, db, team.id, team.name)
	}
	for _, tt := range []struct {
		id, team, tag string
	}{
		{"tt-a-cron", "team-a", "cron"},
		{"tt-a-openhands", "team-a", "openhands"},
		{"tt-b-openhands", "team-b", "openhands"},
		{"tt-b-sh", "team-b", "shell"},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO task_types (task_type_id, team_id, execution_tag) VALUES ($1, $2, $3)`, tt.id, tt.team, tt.tag); err != nil {
			t.Fatalf("seed task type %s: %v", tt.id, err)
		}
	}

	entries, after, err := repo.ListTaskTypes(ctx, platform.AdminTagFilter{}, 200, nil)
	if err != nil {
		t.Fatalf("ListTaskTypes: %v", err)
	}
	if after != nil {
		t.Errorf("expected nil after for finite page; got %+v", after)
	}
	want := []string{
		"team-a|cron|tt-a-cron",
		"team-a|openhands|tt-a-openhands",
		"team-b|openhands|tt-b-openhands",
		"team-b|shell|tt-b-sh",
	}
	if got := tagKeys(entries); !equalStringSlices(got, want) {
		t.Fatalf("ordering: got=%v want=%v", got, want)
	}
}

// TestListTaskTypesPagination walks two pages of size 1 and asserts
// no overlap and no skip across the
// (team_id ASC, execution_tag ASC, task_type_id ASC) ordering.
func TestListTaskTypesPagination(t *testing.T) {
	db := migratedDB(t)
	repo := store.New(db)
	ctx := context.Background()

	for _, team := range []struct {
		id, name string
	}{
		{"team-a", "Team A"},
		{"team-b", "Team B"},
	} {
		mustInsertAdminTeam(t, db, team.id, team.name)
	}
	for _, tt := range []struct {
		id, team, tag string
	}{
		{"tt-a-cron", "team-a", "cron"},
		{"tt-a-openhands", "team-a", "openhands"},
		{"tt-b-openhands", "team-b", "openhands"},
		{"tt-b-shell", "team-b", "shell"},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO task_types (task_type_id, team_id, execution_tag) VALUES ($1, $2, $3)`, tt.id, tt.team, tt.tag); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	seen := []string{}
	cursor := (*store.TaskTypeAfter)(nil)
	for page := 0; page < 8; page++ {
		entries, after, err := repo.ListTaskTypes(ctx, platform.AdminTagFilter{}, 1, cursor)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if len(entries) == 0 {
			break
		}
		seen = append(seen, tagKeys(entries)...)
		if after == nil {
			break
		}
		cursor = after
	}
	want := []string{
		"team-a|cron|tt-a-cron",
		"team-a|openhands|tt-a-openhands",
		"team-b|openhands|tt-b-openhands",
		"team-b|shell|tt-b-shell",
	}
	if !equalStringSlices(seen, want) {
		t.Fatalf("pagination walk: got=%v want=%v", seen, want)
	}
}

func TestListTaskTypesContinuationBoundaries(t *testing.T) {
	db := migratedDB(t)
	repo := store.New(db)
	ctx := context.Background()

	mustInsertAdminTeam(t, db, "team-a", "Team A")
	for _, tt := range []struct{ id, tag string }{
		{"tt-a", "alpha"},
		{"tt-b", "beta"},
		{"tt-c", "gamma"},
	} {
		mustExec(t, db, `INSERT INTO task_types (task_type_id, team_id, execution_tag) VALUES ($1, 'team-a', $2)`, tt.id, tt.tag)
	}

	for _, tc := range []struct {
		name       string
		limit      int
		wantCount  int
		wantCursor bool
	}{
		{name: "limit_below_row_count", limit: 2, wantCount: 2, wantCursor: true},
		{name: "limit_equals_row_count", limit: 3, wantCount: 3, wantCursor: false},
		{name: "limit_above_row_count", limit: 4, wantCount: 3, wantCursor: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries, after, err := repo.ListTaskTypes(ctx, platform.AdminTagFilter{}, tc.limit, nil)
			if err != nil {
				t.Fatalf("ListTaskTypes: %v", err)
			}
			if len(entries) != tc.wantCount {
				t.Fatalf("count=%d want %d", len(entries), tc.wantCount)
			}
			if (after != nil) != tc.wantCursor {
				t.Fatalf("after=%+v wantCursor=%v", after, tc.wantCursor)
			}
		})
	}
}

// TestListTasksOrdering proves the SQL projection is the documented
// 11-field allowlist and that the deterministic
// (ingested_at DESC, task_id DESC) ordering is respected.
func TestListTasksOrdering(t *testing.T) {
	db := migratedDB(t)
	repo := store.New(db)
	ctx := context.Background()

	mustInsertAdminTeam(t, db, "team-a", "Team A")
	mustExec(t, db, `INSERT INTO source_systems (source_system_id, team_id, listener_identity) VALUES ('src-a', 'team-a', 'listener-a')`)
	mustExec(t, db, `INSERT INTO task_types (task_type_id, team_id, execution_tag) VALUES ('tt-a', 'team-a', 'openhands')`)
	mustExec(t, db, `INSERT INTO tasks (task_id, team_id, source_system_id, source_id, task_type_id, required_tag, payload, ingested_at)
		VALUES ('task-latest', 'team-a', 'src-a', 'src-latest', 'tt-a', 'tag-a', '{}'::jsonb, '2024-01-03T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO tasks (task_id, team_id, source_system_id, source_id, task_type_id, required_tag, payload, ingested_at)
		VALUES ('task-middle', 'team-a', 'src-a', 'src-middle', 'tt-a', 'tag-a', '{}'::jsonb, '2024-01-02T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO tasks (task_id, team_id, source_system_id, source_id, task_type_id, required_tag, payload, ingested_at)
		VALUES ('task-earliest', 'team-a', 'src-a', 'src-earliest', 'tt-a', 'tag-a', '{}'::jsonb, '2024-01-01T00:00:00Z')`)

	entries, _, err := repo.ListTasks(ctx, platform.AdminTaskFilter{}, 200, nil)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if want := []string{"task-latest", "task-middle", "task-earliest"}; !idsMatch(entries, want) {
		t.Fatalf("ordering: got=%v want=%v", idsFromEntries(entries), want)
	}
}

// TestListTasksClaimedTimestamp proves non-null timestamptz values are
// scanned as time.Time and normalized to the same UTC RFC3339Nano wire
// representation as ingested_at.
func TestListTasksClaimedTimestamp(t *testing.T) {
	db := migratedDB(t)
	repo := store.New(db)
	ctx := context.Background()

	mustInsertAdminTeam(t, db, "team-a", "Team A")
	mustExec(t, db, `INSERT INTO source_systems (source_system_id, team_id, listener_identity) VALUES ('src-a', 'team-a', 'listener-a')`)
	mustExec(t, db, `INSERT INTO task_types (task_type_id, team_id, execution_tag) VALUES ('tt-a', 'team-a', 'tag-a')`)
	mustExec(t, db, `INSERT INTO executors (executor_id, scope, team_id, executor_type, identity, authorized_tag, max_capacity, running_count)
		VALUES ('exec-a', 'team', 'team-a', 'executor_docker_openhands', 'identity-a', 'tag-a', 1, 1)`)
	mustExec(t, db, `INSERT INTO tasks (
		task_id, team_id, source_system_id, source_id, task_type_id, required_tag, payload,
		current_state, owner_command_id, executor_id, resolved_image, image_source, ingested_at, claimed_at
	) VALUES (
		'task-claimed', 'team-a', 'src-a', 'src-claimed', 'tt-a', 'tag-a', '{}'::jsonb,
		'created', 'cmd-a', 'exec-a', '{"repository":"example/agent","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}'::jsonb,
		'team_default', '2024-01-01T00:00:00Z', '2024-01-01T01:02:03.123456Z'
	)`)

	entries, _, err := repo.ListTasks(ctx, platform.AdminTaskFilter{}, 200, nil)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(entries) != 1 || entries[0].ClaimedAt == nil {
		t.Fatalf("claimed task projection=%+v, want one non-null claimed_at", entries)
	}
	want := time.Date(2024, 1, 1, 1, 2, 3, 123456000, time.UTC).Format(time.RFC3339Nano)
	if got := *entries[0].ClaimedAt; got != want {
		t.Fatalf("claimed_at=%q want %q", got, want)
	}
}

// TestListTasksPagination walks two pages of size 1 and asserts no
// overlap and no skip across the
// (ingested_at DESC, task_id DESC) ordering.
func TestListTasksPagination(t *testing.T) {
	db := migratedDB(t)
	repo := store.New(db)
	ctx := context.Background()

	mustInsertAdminTeam(t, db, "team-a", "Team A")
	mustExec(t, db, `INSERT INTO source_systems (source_system_id, team_id, listener_identity) VALUES ('src-a', 'team-a', 'listener-a')`)
	mustExec(t, db, `INSERT INTO task_types (task_type_id, team_id, execution_tag) VALUES ('tt-a', 'team-a', 'tag-a')`)
	for i, ts := range []string{
		"2024-01-01T00:00:00Z",
		"2024-01-02T00:00:00Z",
		"2024-01-03T00:00:00Z",
	} {
		mustExec(t, db, `INSERT INTO tasks (task_id, team_id, source_system_id, source_id, task_type_id, required_tag, payload, ingested_at)
			VALUES ($1, 'team-a', 'src-a', $2, 'tt-a', 'tag-a', '{}'::jsonb, $3)`,
			"task-"+string(rune('a'+i)), "src-"+string(rune('a'+i)), ts,
		)
	}

	seen := []string{}
	cursor := (*store.TaskAfter)(nil)
	for page := 0; page < 5; page++ {
		entries, after, err := repo.ListTasks(ctx, platform.AdminTaskFilter{}, 1, cursor)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if len(entries) == 0 {
			break
		}
		seen = append(seen, idsFromEntries(entries)...)
		if after == nil {
			break
		}
		cursor = after
	}
	if want := []string{"task-c", "task-b", "task-a"}; !equalStringSlices(seen, want) {
		t.Fatalf("pagination walk: got=%v want=%v", seen, want)
	}
}

func TestListTasksContinuationBoundaries(t *testing.T) {
	db := migratedDB(t)
	repo := store.New(db)
	ctx := context.Background()

	mustInsertAdminTeam(t, db, "team-a", "Team A")
	mustExec(t, db, `INSERT INTO source_systems (source_system_id, team_id, listener_identity) VALUES ('src-a', 'team-a', 'listener-a')`)
	mustExec(t, db, `INSERT INTO task_types (task_type_id, team_id, execution_tag) VALUES ('tt-a', 'team-a', 'tag-a')`)
	for i, ts := range []string{
		"2024-01-01T00:00:00Z",
		"2024-01-02T00:00:00Z",
		"2024-01-03T00:00:00Z",
	} {
		mustExec(t, db, `INSERT INTO tasks (task_id, team_id, source_system_id, source_id, task_type_id, required_tag, payload, ingested_at)
			VALUES ($1, 'team-a', 'src-a', $2, 'tt-a', 'tag-a', '{}'::jsonb, $3)`,
			"task-"+string(rune('a'+i)), "src-"+string(rune('a'+i)), ts,
		)
	}

	for _, tc := range []struct {
		name       string
		limit      int
		wantCount  int
		wantCursor bool
	}{
		{name: "limit_below_row_count", limit: 2, wantCount: 2, wantCursor: true},
		{name: "limit_equals_row_count", limit: 3, wantCount: 3, wantCursor: false},
		{name: "limit_above_row_count", limit: 4, wantCount: 3, wantCursor: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries, after, err := repo.ListTasks(ctx, platform.AdminTaskFilter{}, tc.limit, nil)
			if err != nil {
				t.Fatalf("ListTasks: %v", err)
			}
			if len(entries) != tc.wantCount {
				t.Fatalf("count=%d want %d", len(entries), tc.wantCount)
			}
			if (after != nil) != tc.wantCursor {
				t.Fatalf("after=%+v wantCursor=%v", after, tc.wantCursor)
			}
		})
	}
}

// TestListTasksTeamFilter narrows the deterministic ordering to one
// team and proves the team predicate is applied before pagination.
func TestListTasksTeamFilter(t *testing.T) {
	db := migratedDB(t)
	repo := store.New(db)
	ctx := context.Background()

	mustInsertAdminTeam(t, db, "team-a", "Team A")
	mustInsertAdminTeam(t, db, "team-b", "Team B")
	for _, team := range []string{"team-a", "team-b"} {
		mustExec(t, db, `INSERT INTO source_systems (source_system_id, team_id, listener_identity) VALUES ($1, $2, $3)`, "src-"+team, team, "listener-"+team)
		mustExec(t, db, `INSERT INTO task_types (task_type_id, team_id, execution_tag) VALUES ($1, $2, $3)`, "tt-"+team, team, "tag-"+team)
	}
	mustExec(t, db, `INSERT INTO tasks (task_id, team_id, source_system_id, source_id, task_type_id, required_tag, payload, ingested_at)
		VALUES ('task-a-1', 'team-a', 'src-team-a', 'src-a-1', 'tt-team-a', 'tag-a', '{}'::jsonb, '2024-01-02T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO tasks (task_id, team_id, source_system_id, source_id, task_type_id, required_tag, payload, ingested_at)
		VALUES ('task-a-2', 'team-a', 'src-team-a', 'src-a-2', 'tt-team-a', 'tag-a', '{}'::jsonb, '2024-01-01T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO tasks (task_id, team_id, source_system_id, source_id, task_type_id, required_tag, payload, ingested_at)
		VALUES ('task-b-1', 'team-b', 'src-team-b', 'src-b-1', 'tt-team-b', 'tag-b', '{}'::jsonb, '2024-01-03T00:00:00Z')`)

	entries, _, err := repo.ListTasks(ctx, platform.AdminTaskFilter{TeamID: "team-a"}, 200, nil)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if want := []string{"task-a-1", "task-a-2"}; !idsMatch(entries, want) {
		t.Fatalf("team filter ordering: got=%v want=%v", idsFromEntries(entries), want)
	}
}

// TestListTasksFullCanonicalProjection proves the PostgreSQL
// projection materializes every canonical task column, decodes the
// jsonb payload byte-for-byte, and renders the nullable image fields
// (image, resolved_image, image_source) plus project_id /
// environment_id exactly as persisted.
//
// The seed row covers both nullable and non-null image columns:
//   - task-image-overrides: image=non-null, resolved_image=null,
//     image_source=null (pending row, no claim yet).
//   - task-image-claimed:   image=null, resolved_image=non-null,
//     image_source=team_default, project_id=non-null,
//     environment_id=non-null, payload=non-null.
func TestListTasksFullCanonicalProjection(t *testing.T) {
	db := migratedDB(t)
	repo := store.New(db)
	ctx := context.Background()

	mustInsertAdminTeam(t, db, "team-a", "Team A")
	mustExec(t, db, `INSERT INTO source_systems (source_system_id, team_id, listener_identity) VALUES ('src-a', 'team-a', 'listener-a')`)
	mustExec(t, db, `INSERT INTO task_types (task_type_id, team_id, execution_tag) VALUES ('tt-a', 'team-a', 'tag-a')`)
	mustExec(t, db, `INSERT INTO executors (executor_id, scope, team_id, executor_type, identity, authorized_tag, max_capacity, running_count)
		VALUES ('exec-a', 'team', 'team-a', 'executor_docker_openhands', 'identity-a', 'tag-a', 1, 1)`)

	mustExec(t, db, `INSERT INTO tasks (
		task_id, team_id, source_system_id, source_id, task_type_id,
		required_tag, payload, image,
		current_state, owner_command_id, executor_id,
		project_id, resolved_image, image_source,
		ingested_at, claimed_at
	) VALUES (
		'task-image-overrides', 'team-a', 'src-a', 'src-overrides', 'tt-a',
		'tag-a', $1::jsonb, $2::jsonb,
		'pending', NULL, NULL,
		NULL, NULL, NULL,
		'2024-02-02T00:00:00Z', NULL
	)`,
		`{"k":"v","n":42}`,
		`{"repository":"registry.example/agent:latest","digest":"sha256:`+strings.Repeat("b", 64)+`"}`,
	)

	mustExec(t, db, `INSERT INTO tasks (
		task_id, team_id, source_system_id, source_id, task_type_id,
		required_tag, payload, image,
		current_state, owner_command_id, executor_id,
		project_id, resolved_image, image_source,
		ingested_at, claimed_at
	) VALUES (
		'task-image-claimed', 'team-a', 'src-a', 'src-claimed', 'tt-a',
		'tag-a', $1::jsonb, NULL,
		'created', 'cmd-a', 'exec-a',
		'proj-a', $2::jsonb, 'team_default',
		'2024-02-01T00:00:00Z', '2024-02-01T01:02:03.123456Z'
	)`,
		`{"hello":"world","arr":[1,2,3]}`,
		`{"repository":"registry.example/agent:stable","digest":"sha256:`+strings.Repeat("a", 64)+`"}`,
	)

	entries, _, err := repo.ListTasks(ctx, platform.AdminTaskFilter{}, 200, nil)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries=%d, want 2; rows=%+v", len(entries), entries)
	}

	// The deterministic (ingested_at DESC, task_id DESC) order
	// places task-image-overrides first.
	override := entries[0]
	if override.TaskID != "task-image-overrides" {
		t.Fatalf("first row task_id=%q, want task-image-overrides", override.TaskID)
	}
	if !payloadEquals(override.Payload, `{"k":"v","n":42}`) {
		t.Errorf("override payload=%s, want semantic match for {\"k\":\"v\",\"n\":42}", override.Payload)
	}
	if override.Image == nil {
		t.Fatalf("override Image nil; expected non-null jsonb")
	}
	if override.Image.Repository != "registry.example/agent:latest" {
		t.Errorf("override Image.Repository=%q, want latest", override.Image.Repository)
	}
	if override.ResolvedImage != nil {
		t.Errorf("override ResolvedImage=%+v, want null while pending", override.ResolvedImage)
	}
	if override.ImageSource != nil {
		t.Errorf("override ImageSource=%+v, want null while pending", override.ImageSource)
	}
	if override.ProjectID != nil {
		t.Errorf("override ProjectID=%+v, want null", override.ProjectID)
	}
	if override.EnvironmentID != nil {
		t.Errorf("override EnvironmentID=%+v, want null", override.EnvironmentID)
	}

	claimed := entries[1]
	if claimed.TaskID != "task-image-claimed" {
		t.Fatalf("second row task_id=%q, want task-image-claimed", claimed.TaskID)
	}
	if !payloadEquals(claimed.Payload, `{"hello":"world","arr":[1,2,3]}`) {
		t.Errorf("claimed payload=%s, want semantic match for {\"hello\":\"world\",\"arr\":[1,2,3]}", claimed.Payload)
	}
	if claimed.Image != nil {
		t.Errorf("claimed Image=%+v, want null", claimed.Image)
	}
	if claimed.ResolvedImage == nil {
		t.Fatalf("claimed ResolvedImage nil; expected team_default fallback")
	}
	if claimed.ResolvedImage.Repository != "registry.example/agent:stable" {
		t.Errorf("claimed ResolvedImage.Repository=%q, want stable", claimed.ResolvedImage.Repository)
	}
	if claimed.ImageSource == nil || *claimed.ImageSource != "team_default" {
		t.Errorf("claimed ImageSource=%+v, want team_default", claimed.ImageSource)
	}
	if claimed.ProjectID == nil || *claimed.ProjectID != "proj-a" {
		t.Errorf("claimed ProjectID=%+v, want proj-a", claimed.ProjectID)
	}
	if claimed.EnvironmentID != nil {
		t.Errorf("claimed compatibility EnvironmentID=%+v, want nil after v0006 removal", claimed.EnvironmentID)
	}
}

// payloadEquals compares two JSON payloads by semantic equivalence so
// the assertion holds regardless of PostgreSQL `jsonb` normalization
// (key ordering, whitespace).
func payloadEquals(got json.RawMessage, want string) bool {
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		return false
	}
	gb, _ := json.Marshal(g)
	wb, _ := json.Marshal(w)
	return string(gb) == string(wb)
}

// tagKeys renders the projection as a stable string for comparison.
// The slice is preserved in the order the SQL layer returned it.
func tagKeys(entries []platform.AdminTagEntry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.TeamID+"|"+entry.ExecutionTag+"|"+entry.TaskTypeID)
	}
	return out
}

func idsFromEntries(entries []platform.TaskListEntry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.TaskID)
	}
	return out
}

func idsMatch(entries []platform.TaskListEntry, want []string) bool {
	return equalStringSlices(idsFromEntries(entries), want)
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}
