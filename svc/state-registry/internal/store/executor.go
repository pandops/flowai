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

var (
	ErrExecutorNotFound            = errors.New("executor not found")
	ErrExecutorScopeConflict       = errors.New("executor scope is immutable")
	ErrExecutorTeamConflict        = errors.New("executor team is immutable")
	ErrExecutorIdentityMismatch    = errors.New("executor identity does not match registration")
	ErrExecutorTagMismatch         = errors.New("executor tag mismatch")
	ErrTaskAlreadyClaimed          = errors.New("task already claimed")
	ErrOlderTaskMustBeClaimedFirst = errors.New("older eligible task must be claimed first")
	ErrEventNotFound               = errors.New("event not found")
	ErrEventConflict               = errors.New("event ordering conflict")
	ErrNotAssigned                 = errors.New("executor is not assigned to the task")
	ErrEventTeamMismatch           = errors.New("event envelope team_id does not match")
	ErrInvalidTaskTransition       = errors.New("invalid task transition")
	ErrExecutorNotTeam             = errors.New("executor is not team-scoped")
)

// ExecutorRepository persists registrations and performs read-only discovery.
type ExecutorRepository interface {
	RegisterExecutor(context.Context, string, platform.ExecutorRegistrationRequest, platform.ExecutorIdentity) (platform.Executor, error)
	GetExecutor(context.Context, string) (platform.Executor, error)
	DiscoverExecutorTasks(context.Context, string, string, int) ([]platform.TaskSummary, error)
	ClaimTask(context.Context, platform.ClaimRequest, string, platform.ExecutorIdentity) (platform.ClaimResponse, error)
}

// RegisterExecutor creates or refreshes one Executor while preserving its
// original scope, team binding, and registration timestamp.
func (s *Store) RegisterExecutor(ctx context.Context, executorID string, req platform.ExecutorRegistrationRequest, identity platform.ExecutorIdentity) (platform.Executor, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.Executor{}, fmt.Errorf("begin executor registration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, executorID,
	); err != nil {
		return platform.Executor{}, fmt.Errorf("lock executor registration: %w", err)
	}

	var existingScope string
	var existingTeam sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT scope, team_id FROM executors WHERE executor_id = $1 FOR UPDATE`, executorID,
	).Scan(&existingScope, &existingTeam)
	switch {
	case err == nil:
		if existingScope != req.Scope {
			return platform.Executor{}, ErrExecutorScopeConflict
		}
		if !sameTeam(existingTeam, req.TeamID) {
			return platform.Executor{}, ErrExecutorTeamConflict
		}
	case errors.Is(err, sql.ErrNoRows):
	default:
		return platform.Executor{}, fmt.Errorf("read executor registration: %w", err)
	}

	if identity.ExecutorID != executorID || identity.Scope != req.Scope || !sameTeamPointer(identity.TeamID, req.TeamID) {
		return platform.Executor{}, ErrExecutorIdentityMismatch
	}

	var executor platform.Executor
	var teamID sql.NullString
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `
			INSERT INTO executors (
				executor_id, scope, team_id, executor_type, identity,
				authorized_tag, max_capacity, running_count, runtime_metadata
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb)
			RETURNING executor_id, scope, team_id, executor_type, identity,
				authorized_tag, max_capacity, running_count, runtime_metadata,
				registered_at, updated_at`,
			executorID, req.Scope, req.TeamID, req.ExecutorType, executorID,
			req.AuthorizedTag, req.MaxCapacity, req.RunningCount, req.RuntimeMetadata,
		).Scan(
			&executor.ExecutorID, &executor.Scope, &teamID, &executor.ExecutorType,
			&executor.Identity, &executor.AuthorizedTag, &executor.MaxCapacity,
			&executor.RunningCount, &executor.RuntimeMetadata,
			&executor.RegisteredAt, &executor.UpdatedAt,
		)
	} else {
		err = tx.QueryRowContext(ctx, `
			UPDATE executors
			SET executor_type = $2, identity = $3, authorized_tag = $4,
				max_capacity = $5, running_count = $6,
				runtime_metadata = $7::jsonb, updated_at = now()
			WHERE executor_id = $1
			RETURNING executor_id, scope, team_id, executor_type, identity,
				authorized_tag, max_capacity, running_count, runtime_metadata,
				registered_at, updated_at`,
			executorID, req.ExecutorType, executorID, req.AuthorizedTag,
			req.MaxCapacity, req.RunningCount, req.RuntimeMetadata,
		).Scan(
			&executor.ExecutorID, &executor.Scope, &teamID, &executor.ExecutorType,
			&executor.Identity, &executor.AuthorizedTag, &executor.MaxCapacity,
			&executor.RunningCount, &executor.RuntimeMetadata,
			&executor.RegisteredAt, &executor.UpdatedAt,
		)
	}
	if err != nil {
		if sqlStateIs(err, "23503") {
			return platform.Executor{}, ErrTeamNotFound
		}
		return platform.Executor{}, fmt.Errorf("persist executor registration: %w", err)
	}
	if teamID.Valid {
		executor.TeamID = &teamID.String
	}
	if err := tx.Commit(); err != nil {
		return platform.Executor{}, fmt.Errorf("commit executor registration: %w", err)
	}
	return executor, nil
}

// GetExecutor returns one canonical Executor record.
func (s *Store) GetExecutor(ctx context.Context, executorID string) (platform.Executor, error) {
	var executor platform.Executor
	var teamID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT executor_id, scope, team_id, executor_type, identity,
			authorized_tag, max_capacity, running_count, runtime_metadata,
			registered_at, updated_at
		FROM executors WHERE executor_id = $1`, executorID,
	).Scan(
		&executor.ExecutorID, &executor.Scope, &teamID, &executor.ExecutorType,
		&executor.Identity, &executor.AuthorizedTag, &executor.MaxCapacity,
		&executor.RunningCount, &executor.RuntimeMetadata,
		&executor.RegisteredAt, &executor.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return platform.Executor{}, ErrExecutorNotFound
	}
	if err != nil {
		return platform.Executor{}, fmt.Errorf("get executor: %w", err)
	}
	if teamID.Valid {
		executor.TeamID = &teamID.String
	}
	return executor, nil
}

// DiscoverExecutorTasks returns pending summaries after applying the
// registered scope predicate and then FIFO ordering. It never reads capacity.
func (s *Store) DiscoverExecutorTasks(ctx context.Context, executorID, tag string, limit int) ([]platform.TaskSummary, error) {
	var scope, registeredTag string
	var teamID sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT scope, team_id, authorized_tag FROM executors WHERE executor_id = $1`, executorID,
	).Scan(&scope, &teamID, &registeredTag); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrExecutorNotFound
	} else if err != nil {
		return nil, fmt.Errorf("read executor discovery binding: %w", err)
	}
	if tag != registeredTag {
		return nil, ErrExecutorTagMismatch
	}

	query := `
		SELECT task_id, team_id, required_tag, current_state, ingested_at
		FROM tasks
		WHERE current_state = 'pending' AND required_tag = $1`
	args := []any{registeredTag}
	if scope == platform.ExecutorScopeTeam {
		query += ` AND team_id = $2 ORDER BY ingested_at ASC, task_id ASC LIMIT $3`
		args = append(args, teamID.String, limit)
	} else {
		query += ` ORDER BY ingested_at ASC, task_id ASC LIMIT $2`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("discover executor tasks: %w", err)
	}
	defer rows.Close()

	items := make([]platform.TaskSummary, 0)
	for rows.Next() {
		var item platform.TaskSummary
		if err := rows.Scan(&item.TaskID, &item.TeamID, &item.RequiredTag, &item.CurrentState, &item.IngestedAt); err != nil {
			return nil, fmt.Errorf("scan executor task summary: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate executor task summaries: %w", err)
	}
	return items, nil
}

// ClaimTask atomically assigns the requested pending task to the
// authenticated Executor when:
//
//   - the task is pending,
//   - the task's required_tag matches the Executor's single registered
//     tag,
//   - the eligibility predicate from the Executor's scope holds
//     (same team when scope=team, tag-only when scope=system),
//   - AND the requested task IS the OLDEST currently eligible
//     pending task for the authenticated Executor (FIFO).
//
// Inside one transaction claim:
//
//   - sets tasks.owner_command_id (from the request),
//   - sets tasks.executor_id (immutable thereafter),
//   - persists tasks.resolved_image and tasks.image_source from the
//     four-level precedence chain,
//   - sets tasks.claimed_at,
//   - appends the FIRST lifecycle event `created` with non-null
//     executor_id equal to the claiming Executor and a payload
//     encoded as `task <task_id> loaded by <executor_id>`,
//   - projects the task to current_state='created'.
//
// The function MUST NOT read or evaluate Executor capacity; capacity
// observations never gate the transition.
//
// Conflict taxonomy:
//
//   - ErrTaskNotFound                : unknown / foreign / non-pending
//   - ErrTaskAlreadyClaimed          : different command_id on a
//     visible claimed task
//   - ErrOlderTaskMustBeClaimedFirst : eligible non-oldest pending
//     task for this Executor
//
// Idempotency: a same (task_id, command_id) retry by the original
// Executor returns the original 200 body without appending an event
// or mutating any field.
func (s *Store) ClaimTask(ctx context.Context, req platform.ClaimRequest, executorID string, identity platform.ExecutorIdentity) (platform.ClaimResponse, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.ClaimResponse{}, fmt.Errorf("begin claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Lock the executor row first so concurrent claims against the
	// same Executor serialize on this transaction; tag and scope
	// are immutable from registration so a single read here is
	// stable for the rest of the critical section.
	var (
		scope         string
		teamID        sql.NullString
		registeredTag string
	)
	if err := tx.QueryRowContext(ctx,
		`SELECT scope, team_id, authorized_tag
		   FROM executors
		  WHERE executor_id = $1
		    FOR UPDATE`, executorID,
	).Scan(&scope, &teamID, &registeredTag); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return platform.ClaimResponse{}, ErrExecutorNotFound
		}
		return platform.ClaimResponse{}, fmt.Errorf("read executor for claim: %w", err)
	}
	if identity.ExecutorID != executorID || identity.Scope != scope || !sameClaimTeam(teamID, identity.TeamID) {
		return platform.ClaimResponse{}, ErrExecutorNotFound
	}
	// Every claim for the same tag shares one transaction-scoped lock.
	// Team and system Executors have overlapping eligibility queues, so
	// a narrower team-only lock would not serialize the global FIFO head.
	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"state-registry:claim:"+registeredTag,
	); err != nil {
		return platform.ClaimResponse{}, fmt.Errorf("lock claim eligibility queue: %w", err)
	}

	// Lock the requested task row so a concurrent claim serializes
	// on this transaction; the row stays unchanged when the call
	// resolves to a rejection branch.
	var (
		taskTeam     string
		requiredTag  string
		currentState string
		ownerCommand sql.NullString
		executor     sql.NullString
		claimedAt    sql.NullTime
		sourceSystem string
		sourceID     string
		taskTypeID   string
		imageRow     []byte
		imageSource  sql.NullString
	)
	err = tx.QueryRowContext(ctx, `
		SELECT t.team_id, t.required_tag, t.current_state,
		       t.owner_command_id, t.executor_id, t.claimed_at,
		       t.source_system_id, t.source_id, t.task_type_id,
		       t.image, t.image_source
		  FROM tasks t
		 WHERE t.task_id = $1
		    FOR UPDATE`, req.TaskID,
	).Scan(&taskTeam, &requiredTag, &currentState,
		&ownerCommand, &executor, &claimedAt,
		&sourceSystem, &sourceID, &taskTypeID,
		&imageRow, &imageSource)
	if errors.Is(err, sql.ErrNoRows) {
		return platform.ClaimResponse{}, ErrTaskNotFound
	}
	if err != nil {
		return platform.ClaimResponse{}, fmt.Errorf("lock task for claim: %w", err)
	}

	// Same-team visibility for team-owned Executors.
	if scope == platform.ExecutorScopeTeam && (teamID.String == "" || teamID.String != taskTeam) {
		return platform.ClaimResponse{}, ErrTaskNotFound
	}
	if registeredTag != requiredTag {
		return platform.ClaimResponse{}, ErrTaskNotFound
	}

	// Idempotent retry: same (task_id, command_id) returns the
	// original 200 with no event re-append. The branch is gated
	// on team visibility BEFORE distinguishing the conflict
	// taxonomy: foreign-team names on a non-pending task always
	// collapse to ErrTaskNotFound per rule (a)/(e); same-team
	// submits that miss the (task_id, command_id) tuple return
	// ErrTaskAlreadyClaimed per rule (b).
	if currentState != platform.TaskStatePending {
		if scope == platform.ExecutorScopeTeam && (teamID.String == "" || teamID.String != taskTeam) {
			return platform.ClaimResponse{}, ErrTaskNotFound
		}
		if ownerCommand.Valid && executor.Valid &&
			ownerCommand.String == req.CommandID && executor.String == executorID {
			return s.buildClaimResponseForRow(tx, ctx, req.TaskID)
		}
		return platform.ClaimResponse{}, ErrTaskAlreadyClaimed
	}

	// FIFO: the requested task MUST be the oldest currently
	// eligible pending task for the authenticated Executor. The
	// probe re-reads the row's ingested_at inside the transaction
	// so it stays exact against committed data.
	var currentIngested time.Time
	if err := tx.QueryRowContext(ctx,
		`SELECT ingested_at FROM tasks WHERE task_id = $1`, req.TaskID,
	).Scan(&currentIngested); err != nil {
		return platform.ClaimResponse{}, fmt.Errorf("read task ingested_at: %w", err)
	}
	earlier, err := isEarlierPendingTask(tx, ctx, registeredTag, scope, teamID, currentIngested, req.TaskID)
	if err != nil {
		return platform.ClaimResponse{}, fmt.Errorf("check older eligible task: %w", err)
	}
	if earlier {
		return platform.ClaimResponse{}, ErrOlderTaskMustBeClaimedFirst
	}

	// Resolve the effective image from the four-level precedence
	// chain inside the same transaction so the persisted values
	// reflect the canonical image at commit time.
	launchParameters, err := resolveLaunchParametersLocked(ctx, tx, taskTeam, taskTypeID, req.TaskID)
	if err != nil {
		return platform.ClaimResponse{}, fmt.Errorf("resolve launch parameters: %w", err)
	}
	resolvedImage, imageSourceStr, err := resolveClaimImageLocked(ctx, tx, taskTeam, sourceSystem, taskTypeID, imageRow, launchParameters.Image, launchParameters.ImageSource)
	if err != nil {
		return platform.ClaimResponse{}, fmt.Errorf("resolve claim image: %w", err)
	}

	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		UPDATE tasks
		   SET current_state    = 'created',
		       owner_command_id = $2,
		       executor_id      = $3,
		       claimed_at       = $4,
		       resolved_image   = $5::jsonb,
		       image_source     = $6
		 WHERE task_id = $1`, req.TaskID, req.CommandID, executorID,
		now, resolvedImage, imageSourceStr,
	); err != nil {
		return platform.ClaimResponse{}, fmt.Errorf("persist claim assignment: %w", err)
	}

	payload, err := json.Marshal(map[string]string{
		"message":  fmt.Sprintf("task %s loaded by %s", req.TaskID, executorID),
		"task_id":  req.TaskID,
		"executor": executorID,
	})
	if err != nil {
		return platform.ClaimResponse{}, fmt.Errorf("marshal created payload: %w", err)
	}
	eventID := "evt-" + req.TaskID
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO task_events
		 (event_id, team_id, task_id, executor_id, event_type, occurred_at, payload)
		 VALUES ($1, $2, $3, $4, 'created', $5, $6::jsonb)`,
		eventID, taskTeam, req.TaskID, executorID, now, string(payload),
	); err != nil {
		return platform.ClaimResponse{}, fmt.Errorf("append created event: %w", err)
	}
	response, err := s.buildClaimResponseForRow(tx, ctx, req.TaskID)
	if err != nil {
		return platform.ClaimResponse{}, fmt.Errorf("build committed claim response: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return platform.ClaimResponse{}, fmt.Errorf("commit claim: %w", err)
	}
	return response, nil
}

// buildClaimResponseForRow rebuilds the canonical claim envelope for
// the (task_id, command_id) idempotent retry case. The function
// reads the task row inside the supplied transaction so the original
// accepted owner_command_id, executor_id, resolved_image,
// image_source, and claimed_at round-trip faithfully.
func (s *Store) buildClaimResponseForRow(tx *sql.Tx, ctx context.Context, taskID string) (platform.ClaimResponse, error) {
	entry, _, err := scanTaskRow(tx.QueryRowContext(ctx, `
		SELECT task_id, team_id, source_system_id, source_id,
		       task_type_id, required_tag, payload, current_state,
		       owner_command_id, executor_id, project_id,
		       image, resolved_image, image_source, ingested_at, claimed_at
		  FROM tasks WHERE task_id = $1`, taskID,
	))
	if err != nil {
		return platform.ClaimResponse{}, fmt.Errorf("re-read canonical task for retry: %w", err)
	}
	response := platform.ClaimResponse{
		Claim:         "claimed",
		Task:          entry,
		ResolvedImage: entry.ResolvedImage,
		ImageSource:   entry.ImageSource,
		ClaimedAt:     entry.ClaimedAt,
	}
	var hasLaunchParameters bool
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((snap.values <> '{}'::jsonb) OR EXISTS (
		SELECT 1 FROM task_launch_parameter_secret_refs ref WHERE ref.task_id = $1
	), false) FROM task_launch_parameter_snapshots snap WHERE snap.task_id = $1`, taskID).Scan(&hasLaunchParameters); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return response, nil
		}
		return platform.ClaimResponse{}, fmt.Errorf("read launch parameter snapshot discriminator: %w", err)
	}
	if !hasLaunchParameters {
		return response, nil
	}
	response.ScopeToken, err = s.issueScopeToken(ctx, tx, entry)
	response.LaunchParameters = response.ScopeToken != nil
	return response, err
}

// isEarlierPendingTask returns true when at least one pending task
// eligible for the supplied scope precedes the requested task by
// (ingested_at, task_id). The function is called inside the same
// claim transaction so the probe is exact against committed data.
func isEarlierPendingTask(tx *sql.Tx, ctx context.Context, tag, scope string, teamID sql.NullString, ingestedAt time.Time, taskID string) (bool, error) {
	var exists bool
	if scope == platform.ExecutorScopeTeam {
		err := tx.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM tasks
				 WHERE current_state = 'pending'
				   AND required_tag = $1
				   AND team_id = $2
				   AND (ingested_at, task_id) < ($3, $4)
			)`, tag, teamID.String, ingestedAt, taskID,
		).Scan(&exists)
		if err != nil {
			return false, err
		}
		return exists, nil
	}
	err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM tasks
			 WHERE current_state = 'pending'
			   AND required_tag = $1
			   AND (ingested_at, task_id) < ($2, $3)
		)`, tag, ingestedAt, taskID,
	).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// resolveClaimImageLocked walks the documented four-level precedence
// chain inside the claim transaction. The order is invariant:
// tasks.image (override) -> task_types.default_image ->
// source_systems.default_image -> teams.default_image (always
// non-null because admin registration requires it). The returned
// values are the persisted jsonb bytes (or nil) and the matching
// image_source discriminator.
func resolveClaimImageLocked(ctx context.Context, tx *sql.Tx, teamID, sourceSystem, taskType string, taskImage []byte, launchImage *string, launchImageSource string) (json.RawMessage, string, error) {
	if len(taskImage) > 0 {
		encoded, err := json.Marshal(json.RawMessage(taskImage))
		if err != nil {
			return nil, "", err
		}
		return encoded, platform.ImageSourceTaskOverride, nil
	}
	if launchImage != nil {
		separator := strings.LastIndex(*launchImage, "@")
		if separator <= 0 || separator == len(*launchImage)-1 {
			return nil, "", errors.New("launch parameter image is invalid")
		}
		encoded, err := json.Marshal(platform.ImageReference{Repository: (*launchImage)[:separator], Digest: (*launchImage)[separator+1:]})
		if err != nil {
			return nil, "", fmt.Errorf("marshal launch parameter image: %w", err)
		}
		return encoded, launchImageSource, nil
	}
	var ttDefault []byte
	if err := tx.QueryRowContext(ctx,
		`SELECT default_image::text FROM task_types WHERE team_id = $1 AND task_type_id = $2`,
		teamID, taskType,
	).Scan(&ttDefault); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, "", err
	}
	if len(ttDefault) > 0 {
		return json.RawMessage(ttDefault), platform.ImageSourceTaskTypeDefault, nil
	}
	var ssDefault []byte
	if err := tx.QueryRowContext(ctx,
		`SELECT default_image::text FROM source_systems WHERE team_id = $1 AND source_system_id = $2`,
		teamID, sourceSystem,
	).Scan(&ssDefault); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, "", err
	}
	if len(ssDefault) > 0 {
		return json.RawMessage(ssDefault), platform.ImageSourceSourceSystemDefault, nil
	}
	var teamDefault []byte
	if err := tx.QueryRowContext(ctx,
		`SELECT default_image::text FROM teams WHERE team_id = $1`, teamID,
	).Scan(&teamDefault); err != nil {
		return nil, "", err
	}
	return json.RawMessage(teamDefault), platform.ImageSourceTeamDefault, nil
}

func sameClaimTeam(existing sql.NullString, requested *string) bool {
	return existing.Valid == (requested != nil) && (!existing.Valid || existing.String == *requested)
}

func sameTeam(existing sql.NullString, requested *string) bool {
	return existing.Valid == (requested != nil) && (!existing.Valid || existing.String == *requested)
}

func sameTeamPointer(left, right *string) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
}

var _ ExecutorRepository = (*Store)(nil)
