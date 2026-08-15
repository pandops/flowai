package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
)

type UIAfter struct {
	At time.Time
	ID string
}

var ErrUIResourceUnknown = errors.New("UI resource unknown or unavailable")

type UIOperatorRepository interface {
	GetUIDashboard(context.Context, string, string, string, time.Time) (platform.UIDashboard, error)
	ListUITasks(context.Context, string, platform.UITaskFilter, int, *UIAfter) ([]platform.UITask, *UIAfter, error)
	GetUITask(context.Context, string, string) (platform.UITask, error)
	ListUITaskEvents(context.Context, string, string, int, *UIAfter) ([]platform.UIEvent, *UIAfter, error)
	ListUIExecutors(context.Context, string, platform.UIExecutorFilter, int, *UIAfter) ([]platform.UIExecutor, *UIAfter, error)
	GetUIExecutor(context.Context, string, string) (platform.UIExecutor, error)
	ListUIExecutorEvents(context.Context, string, string, int, *UIAfter) ([]platform.UIEvent, *UIAfter, error)
	ListUIExecutorTasks(context.Context, string, string, string, int, *UIAfter) ([]platform.UITask, *UIAfter, error)
	ListUIAudit(context.Context, string, platform.UIAuditFilter, int, *UIAfter) ([]platform.AuditEntry, *UIAfter, error)
}

func (s *Store) GetUIDashboard(ctx context.Context, teamID, period, calendarKey string, now time.Time) (platform.UIDashboard, error) {
	window, err := uiDashboardWindow(period, calendarKey, now)
	if err != nil {
		return platform.UIDashboard{}, err
	}
	result := platform.UIDashboard{Period: period, CalendarKey: window.key, Counts: uiStateCounts(), Buckets: window.buckets(now)}
	rows, err := s.db.QueryContext(ctx, `SELECT current_state, count(*) FROM tasks
		WHERE team_id = $1 AND ingested_at >= $2 AND ingested_at < $3 GROUP BY current_state`, teamID, window.start, window.end)
	if err != nil {
		return result, fmt.Errorf("read UI dashboard counts: %w", err)
	}
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			rows.Close()
			return result, fmt.Errorf("scan UI dashboard count: %w", err)
		}
		result.Counts[state] = count
	}
	if err := rows.Close(); err != nil {
		return result, fmt.Errorf("close UI dashboard counts: %w", err)
	}
	rows, err = s.db.QueryContext(ctx, `SELECT floor(extract(epoch FROM (ingested_at - $2)) / 86400)::int AS bucket_index, current_state, count(*)
		FROM tasks WHERE team_id = $1 AND ingested_at >= $2 AND ingested_at < $3
		GROUP BY bucket_index, current_state ORDER BY bucket_index ASC`, teamID, window.start, window.end)
	if err != nil {
		return result, fmt.Errorf("read UI dashboard buckets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var index int
		var state string
		var count int
		if err := rows.Scan(&index, &state, &count); err != nil {
			return result, fmt.Errorf("scan UI dashboard bucket: %w", err)
		}
		if index < 0 || index >= len(result.Buckets) {
			continue
		}
		result.Buckets[index].Counts[state] = count
		result.Buckets[index].Total += count
	}
	return result, rows.Err()
}

type dashboardWindow struct {
	period string
	key    string
	start  time.Time
	end    time.Time
}

func uiDashboardWindow(period, calendarKey string, now time.Time) (dashboardWindow, error) {
	now = now.UTC()
	switch period {
	case "week":
		currentYear, currentWeek := now.ISOWeek()
		if calendarKey == "" {
			calendarKey = fmt.Sprintf("%04d-W%02d", currentYear, currentWeek)
		}
		var year, week int
		if _, err := fmt.Sscanf(calendarKey, "%04d-W%02d", &year, &week); err != nil {
			return dashboardWindow{}, errors.New("invalid calendar key")
		}
		if fmt.Sprintf("%04d-W%02d", year, week) != calendarKey {
			return dashboardWindow{}, errors.New("invalid calendar key")
		}
		start := isoWeekStart(year, week)
		parsedYear, parsedWeek := start.ISOWeek()
		if parsedYear != year || parsedWeek != week || start.After(isoWeekStart(currentYear, currentWeek)) {
			return dashboardWindow{}, errors.New("invalid calendar key")
		}
		return dashboardWindow{period: period, key: fmt.Sprintf("%04d-W%02d", year, week), start: start, end: start.AddDate(0, 0, 7)}, nil
	case "month":
		currentStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		if calendarKey == "" {
			calendarKey = currentStart.Format("2006-01")
		}
		start, err := time.ParseInLocation("2006-01", calendarKey, time.UTC)
		if err != nil || start.After(currentStart) {
			return dashboardWindow{}, errors.New("invalid calendar key")
		}
		return dashboardWindow{period: period, key: start.Format("2006-01"), start: start, end: start.AddDate(0, 1, 0)}, nil
	default:
		return dashboardWindow{}, errors.New("invalid period")
	}
}

func isoWeekStart(year, week int) time.Time {
	januaryFourth := time.Date(year, time.January, 4, 0, 0, 0, 0, time.UTC)
	return januaryFourth.AddDate(0, 0, -(int(januaryFourth.Weekday())+6)%7+(week-1)*7)
}

func (window dashboardWindow) buckets(now time.Time) []platform.UIDashboardBucket {
	labels := [...]string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
	count := int(window.end.Sub(window.start) / (24 * time.Hour))
	buckets := make([]platform.UIDashboardBucket, 0, count)
	for index := 0; index < count; index++ {
		at := window.start.AddDate(0, 0, index)
		label := fmt.Sprintf("%d", at.Day())
		if window.period == "week" {
			label = labels[index]
		}
		buckets = append(buckets, platform.UIDashboardBucket{
			At: at, Key: at.Format(time.DateOnly), Label: label, ShortLabel: label,
			Counts: uiStateCounts(), IsFuture: at.After(now.UTC()),
		})
	}
	return buckets
}

func uiStateCounts() map[string]int {
	return map[string]int{"pending": 0, "created": 0, "running": 0, "finished": 0, "failed": 0}
}

func uiPeriod(period string, now time.Time) (time.Time, string, error) {
	now = now.UTC()
	switch period {
	case "day":
		return now.Add(-24 * time.Hour), "hour", nil
	case "week":
		return now.Add(-7 * 24 * time.Hour), "day", nil
	case "month":
		return now.AddDate(0, -1, 0), "day", nil
	default:
		return time.Time{}, "", errors.New("invalid period")
	}
}

func (s *Store) ListUITasks(ctx context.Context, teamID string, filter platform.UITaskFilter, limit int, after *UIAfter) ([]platform.UITask, *UIAfter, error) {
	return s.listUITasks(ctx, teamID, "", filter, limit, after)
}

func (s *Store) ListUIExecutorTasks(ctx context.Context, teamID, executorID, status string, limit int, after *UIAfter) ([]platform.UITask, *UIAfter, error) {
	if _, err := s.GetUIExecutor(ctx, teamID, executorID); err != nil {
		return nil, nil, err
	}
	return s.listUITasks(ctx, teamID, executorID, platform.UITaskFilter{Status: status}, limit, after)
}

func (s *Store) listUITasks(ctx context.Context, teamID, executorID string, filter platform.UITaskFilter, limit int, after *UIAfter) ([]platform.UITask, *UIAfter, error) {
	clauses := []string{"t.team_id = $1"}
	args := []any{teamID}
	if executorID != "" {
		args = append(args, executorID)
		clauses = append(clauses, fmt.Sprintf("t.executor_id = $%d", len(args)))
	}
	if filter.Status != "" {
		args = append(args, filter.Status)
		clauses = append(clauses, fmt.Sprintf("t.current_state = $%d", len(args)))
	}
	if filter.Search != "" {
		args = append(args, "%"+filter.Search+"%")
		p := len(args)
		clauses = append(clauses, fmt.Sprintf("(t.task_id ILIKE $%d OR t.task_type_id ILIKE $%d OR t.source_id ILIKE $%d OR COALESCE(t.executor_id,'') ILIKE $%d)", p, p, p, p))
	}
	if filter.Period != "" {
		since, _, err := uiPeriod(filter.Period, time.Now())
		if err != nil {
			return nil, nil, err
		}
		args = append(args, since)
		clauses = append(clauses, fmt.Sprintf("t.ingested_at >= $%d", len(args)))
	}
	if filter.Date != "" {
		start, err := time.ParseInLocation(time.DateOnly, filter.Date, time.UTC)
		if err != nil {
			return nil, nil, errors.New("invalid date")
		}
		args = append(args, start, start.AddDate(0, 0, 1))
		clauses = append(clauses, fmt.Sprintf("t.ingested_at >= $%d AND t.ingested_at < $%d", len(args)-1, len(args)))
	}
	if after != nil {
		args = append(args, after.At, after.At, after.ID)
		clauses = append(clauses, fmt.Sprintf("(t.ingested_at < $%d OR (t.ingested_at = $%d AND t.task_id < $%d))", len(args)-2, len(args)-1, len(args)))
	}
	args = append(args, limit+1)
	query := fmt.Sprintf(`SELECT t.task_id, t.task_type_id, t.source_system_id, t.source_id,
		t.current_state, t.executor_id, t.ingested_at, t.claimed_at,
		(SELECT max(te.occurred_at) FROM task_events te WHERE te.task_id=t.task_id AND te.event_type IN ('finished','failed')),
		COALESCE(snap.values, '{}'::jsonb), COALESCE((SELECT jsonb_agg(ref.key ORDER BY ref.key)
		 FROM task_launch_parameter_secret_refs ref WHERE ref.task_id=t.task_id), '[]'::jsonb)
		FROM tasks t LEFT JOIN task_launch_parameter_snapshots snap ON snap.task_id=t.task_id
		WHERE %s ORDER BY t.ingested_at DESC, t.task_id DESC LIMIT $%d`, strings.Join(clauses, " AND "), len(args))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("list UI tasks: %w", err)
	}
	defer rows.Close()
	items := make([]platform.UITask, 0, limit+1)
	for rows.Next() {
		item, err := scanUITask(rows)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(items) <= limit {
		return items, nil, nil
	}
	last := items[limit-1]
	return items[:limit], &UIAfter{At: last.IngestedAt, ID: last.TaskID}, nil
}

func (s *Store) GetUITask(ctx context.Context, teamID, taskID string) (platform.UITask, error) {
	row := s.db.QueryRowContext(ctx, `SELECT t.task_id, t.task_type_id, t.source_system_id, t.source_id,
		t.current_state, t.executor_id, t.ingested_at, t.claimed_at,
		(SELECT max(te.occurred_at) FROM task_events te WHERE te.task_id=t.task_id AND te.event_type IN ('finished','failed')),
		COALESCE(snap.values, '{}'::jsonb), COALESCE((SELECT jsonb_agg(ref.key ORDER BY ref.key)
		 FROM task_launch_parameter_secret_refs ref WHERE ref.task_id=t.task_id), '[]'::jsonb)
		FROM tasks t LEFT JOIN task_launch_parameter_snapshots snap ON snap.task_id=t.task_id
		WHERE t.team_id=$1 AND t.task_id=$2`, teamID, taskID)
	item, err := scanUITask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return platform.UITask{}, ErrTaskUnknown
	}
	return item, err
}

type uiTaskScanner interface{ Scan(...any) error }

func scanUITask(row uiTaskScanner) (platform.UITask, error) {
	var item platform.UITask
	var executor sql.NullString
	var claimed, completed sql.NullTime
	var envRaw, secretsRaw []byte
	if err := row.Scan(&item.TaskID, &item.TaskTypeID, &item.SourceSystemID, &item.SourceID,
		&item.CurrentState, &executor, &item.IngestedAt, &claimed, &completed, &envRaw, &secretsRaw); err != nil {
		return item, err
	}
	if executor.Valid {
		item.ExecutorID = &executor.String
	}
	if claimed.Valid {
		t := claimed.Time.UTC()
		item.ClaimedAt = &t
	}
	if completed.Valid {
		t := completed.Time.UTC()
		item.CompletedAt = &t
	}
	item.IngestedAt = item.IngestedAt.UTC()
	item.Environment = map[string]string{}
	item.SecretKeys = []string{}
	if err := json.Unmarshal(envRaw, &item.Environment); err != nil {
		return item, fmt.Errorf("decode UI task environment: %w", err)
	}
	if err := json.Unmarshal(secretsRaw, &item.SecretKeys); err != nil {
		return item, fmt.Errorf("decode UI task secret keys: %w", err)
	}
	return item, nil
}

func (s *Store) ListUITaskEvents(ctx context.Context, teamID, taskID string, limit int, after *UIAfter) ([]platform.UIEvent, *UIAfter, error) {
	if _, err := s.GetUITask(ctx, teamID, taskID); err != nil {
		return nil, nil, err
	}
	args := []any{teamID, taskID}
	afterClause := ""
	if after != nil {
		args = append(args, after.At, after.At, after.ID)
		afterClause = fmt.Sprintf("WHERE (occurred_at > $%d OR (occurred_at = $%d AND event_id > $%d))", len(args)-2, len(args)-1, len(args))
	}
	args = append(args, limit+1)
	query := fmt.Sprintf(`SELECT event_id, event_type, status, executor_id, occurred_at, payload FROM (
		SELECT event_id, 'task.lifecycle.'||event_type AS event_type, event_type AS status, executor_id, occurred_at, payload
		FROM task_events WHERE team_id=$1 AND task_id=$2
		UNION ALL
		SELECT control_event_id, 'task.control.requested', status, executor_id, occurred_at, payload
		FROM task_control_events WHERE team_id=$1 AND task_id=$2
	) feed %s ORDER BY occurred_at ASC, event_id ASC LIMIT $%d`, afterClause, len(args))
	return scanUIEvents(ctx, s.db, query, limit, args...)
}

func (s *Store) ListUIExecutors(ctx context.Context, teamID string, filter platform.UIExecutorFilter, limit int, after *UIAfter) ([]platform.UIExecutor, *UIAfter, error) {
	clauses := []string{"(e.team_id=$1 OR (e.scope='system' AND EXISTS (SELECT 1 FROM task_types tt WHERE tt.team_id=$1 AND tt.execution_tag=e.authorized_tag)))"}
	args := []any{teamID}
	switch filter.Status {
	case "":
	case "busy":
		clauses = append(clauses, "e.running_count > 0")
	case "idle":
		clauses = append(clauses, "e.running_count = 0")
	default:
		return nil, nil, errors.New("invalid executor status")
	}
	if filter.Search != "" {
		args = append(args, "%"+filter.Search+"%")
		p := len(args)
		clauses = append(clauses, fmt.Sprintf("(e.executor_id ILIKE $%d OR e.executor_type ILIKE $%d OR e.authorized_tag ILIKE $%d)", p, p, p))
	}
	if after != nil {
		args = append(args, after.At, after.At, after.ID)
		clauses = append(clauses, fmt.Sprintf("(e.updated_at < $%d OR (e.updated_at = $%d AND e.executor_id < $%d))", len(args)-2, len(args)-1, len(args)))
	}
	args = append(args, limit+1)
	query := fmt.Sprintf(`SELECT e.executor_id,e.executor_type,e.scope,e.authorized_tag,
		CASE WHEN e.running_count>0 THEN 'busy' ELSE 'idle' END,e.max_capacity,e.running_count,
		GREATEST(e.max_capacity-e.running_count,0),e.runtime_metadata,e.updated_at
		FROM executors e WHERE %s ORDER BY e.updated_at DESC,e.executor_id DESC LIMIT $%d`, strings.Join(clauses, " AND "), len(args))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("list UI executors: %w", err)
	}
	defer rows.Close()
	items := make([]platform.UIExecutor, 0, limit+1)
	for rows.Next() {
		var item platform.UIExecutor
		if err := rows.Scan(&item.ExecutorID, &item.ExecutorType, &item.Scope, &item.AuthorizedTag,
			&item.Status, &item.MaxCapacity, &item.RunningCount, &item.Available,
			&item.RuntimeMetadata, &item.UpdatedAt); err != nil {
			return nil, nil, fmt.Errorf("scan UI executor: %w", err)
		}
		items = append(items, item)
	}
	if len(items) <= limit {
		return items, nil, rows.Err()
	}
	last := items[limit-1]
	return items[:limit], &UIAfter{At: last.UpdatedAt, ID: last.ExecutorID}, rows.Err()
}

func (s *Store) GetUIExecutor(ctx context.Context, teamID, executorID string) (platform.UIExecutor, error) {
	var item platform.UIExecutor
	err := s.db.QueryRowContext(ctx, `SELECT e.executor_id,e.executor_type,e.scope,e.authorized_tag,
		CASE WHEN e.running_count>0 THEN 'busy' ELSE 'idle' END,e.max_capacity,e.running_count,
		GREATEST(e.max_capacity-e.running_count,0),e.runtime_metadata,e.updated_at
		FROM executors e WHERE e.executor_id=$2 AND
		(e.team_id=$1 OR (e.scope='system' AND EXISTS (SELECT 1 FROM task_types tt WHERE tt.team_id=$1 AND tt.execution_tag=e.authorized_tag)))`, teamID, executorID).
		Scan(&item.ExecutorID, &item.ExecutorType, &item.Scope, &item.AuthorizedTag, &item.Status,
			&item.MaxCapacity, &item.RunningCount, &item.Available, &item.RuntimeMetadata, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrUIResourceUnknown
	}
	return item, err
}

func (s *Store) ListUIExecutorEvents(ctx context.Context, teamID, executorID string, limit int, after *UIAfter) ([]platform.UIEvent, *UIAfter, error) {
	if _, err := s.GetUIExecutor(ctx, teamID, executorID); err != nil {
		return nil, nil, err
	}
	args := []any{executorID}
	clause := ""
	if after != nil {
		args = append(args, after.At, after.At, after.ID)
		clause = fmt.Sprintf("AND (occurred_at > $%d OR (occurred_at=$%d AND event_id>$%d))", len(args)-2, len(args)-1, len(args))
	}
	args = append(args, limit+1)
	query := fmt.Sprintf(`SELECT event_id,'executor.'||event_type,event_type,NULL::text,occurred_at,payload
		FROM executor_events WHERE executor_id=$1 %s ORDER BY occurred_at ASC,event_id ASC LIMIT $%d`, clause, len(args))
	return scanUIEvents(ctx, s.db, query, limit, args...)
}

type uiEventQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func scanUIEvents(ctx context.Context, db uiEventQuerier, query string, limit int, args ...any) ([]platform.UIEvent, *UIAfter, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items := make([]platform.UIEvent, 0, limit+1)
	for rows.Next() {
		var item platform.UIEvent
		var executor sql.NullString
		if err := rows.Scan(&item.EventID, &item.EventType, &item.Status, &executor, &item.OccurredAt, &item.Payload); err != nil {
			return nil, nil, err
		}
		if executor.Valid {
			item.ExecutorID = &executor.String
		}
		items = append(items, item)
	}
	if len(items) <= limit {
		return items, nil, rows.Err()
	}
	last := items[limit-1]
	return items[:limit], &UIAfter{At: last.OccurredAt, ID: last.EventID}, rows.Err()
}

func (s *Store) ListUIAudit(ctx context.Context, teamID string, filter platform.UIAuditFilter, limit int, after *UIAfter) ([]platform.AuditEntry, *UIAfter, error) {
	clauses := []string{"team_id=$1"}
	args := []any{teamID}
	if filter.Search != "" {
		args = append(args, "%"+filter.Search+"%")
		p := len(args)
		clauses = append(clauses, fmt.Sprintf("(action ILIKE $%d OR actor_id ILIKE $%d OR resource_id ILIKE $%d)", p, p, p))
	}
	if after != nil {
		args = append(args, after.At, after.At, after.ID)
		clauses = append(clauses, fmt.Sprintf("(occurred_at < $%d OR (occurred_at=$%d AND audit_id<$%d))", len(args)-2, len(args)-1, len(args)))
	}
	args = append(args, limit+1)
	query := fmt.Sprintf(`SELECT audit_id,team_id,actor_id,actor_type,action,resource_type,resource_id,request_id,outcome,occurred_at
		FROM audit_entries WHERE %s ORDER BY occurred_at DESC,audit_id DESC LIMIT $%d`, strings.Join(clauses, " AND "), len(args))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items := make([]platform.AuditEntry, 0, limit+1)
	for rows.Next() {
		var item platform.AuditEntry
		if err := rows.Scan(&item.AuditID, &item.TeamID, &item.ActorID, &item.ActorType,
			&item.Action, &item.ResourceType, &item.ResourceID, &item.RequestID,
			&item.Outcome, &item.OccurredAt); err != nil {
			return nil, nil, err
		}
		items = append(items, item)
	}
	if len(items) <= limit {
		return items, nil, rows.Err()
	}
	last := items[limit-1]
	return items[:limit], &UIAfter{At: last.OccurredAt, ID: last.AuditID}, rows.Err()
}

var _ UIOperatorRepository = (*Store)(nil)
