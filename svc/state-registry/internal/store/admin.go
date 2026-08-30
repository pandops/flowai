package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
)

var (
	// ErrTeamNameConflict reports that a team already uses the requested
	// display name.
	ErrTeamNameConflict = errors.New("team name already exists")
	// ErrListenerIdentityConflict reports that a listener principal is already
	// bound to a source system.
	ErrListenerIdentityConflict = errors.New("listener identity already bound")
	// ErrTeamNotFound reports that a source system or task type references an
	// unknown team.
	ErrTeamNotFound = errors.New("team not found")
)

// AdminRepository is the persistence contract consumed by the admin HTTP
// boundary.
type AdminRepository interface {
	CreateTeam(context.Context, platform.CreateTeamRequest, platform.AdminIdentity) (platform.Team, error)
	CreateSourceSystem(context.Context, platform.CreateSourceSystemRequest, platform.AdminIdentity) (platform.SourceSystem, error)
	CreateTaskType(context.Context, platform.CreateTaskTypeRequest, platform.AdminIdentity) (platform.TaskType, error)
	TeamExists(context.Context, string) (bool, error)
}

type TeamLifecycleRepository interface {
	GetTeam(context.Context, string) (platform.Team, error)
	UpdateTeam(context.Context, string, platform.UpdateTeamRequest, platform.AdminIdentity) (platform.Team, error)
	ArchiveTeam(context.Context, string, platform.AdminIdentity) (platform.Team, bool, error)
}

// Store persists State Registry resources in PostgreSQL.
type Store struct {
	db                *sql.DB
	aesKey            []byte
	scopeTokenKeyring *scopeTokenKeyring
	barriers          TransactionBarriers
}
type TransactionBarriers interface{ Wait(context.Context, string) }

func (s *Store) WithTransactionBarriers(barriers TransactionBarriers) *Store {
	s.barriers = barriers
	return s
}
func (s *Store) waitBarrier(ctx context.Context, name string) {
	if s.barriers != nil {
		s.barriers.Wait(ctx, name)
	}
}

// New returns a PostgreSQL-backed Store.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

func NewWithAESKey(db *sql.DB, key []byte) *Store {
	keyCopy := append([]byte(nil), key...)
	return &Store{db: db, aesKey: keyCopy}
}

// NewWithScopeTokenKeyring wires the AES-256-GCM key and the
// rotating scope-token HMAC keyring into a Store. Production
// startup uses this constructor; callers that only need the AES path
// can stay on NewWithAESKey.
func NewWithScopeTokenKeyring(db *sql.DB, aesKey []byte, keyring *ScopeTokenKeyring) *Store {
	if keyring == nil || keyring.inner == nil {
		return &Store{db: db, aesKey: append([]byte(nil), aesKey...)}
	}
	keyCopy := append([]byte(nil), aesKey...)
	return &Store{db: db, aesKey: keyCopy, scopeTokenKeyring: keyring.inner}
}

// TeamExists reports whether teamID names a registered team. It performs no
// mutation and is used to reject runtime registration against unknown teams.
func (s *Store) TeamExists(ctx context.Context, teamID string) (bool, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM teams WHERE team_id = $1)`, teamID,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("check team existence: %w", err)
	}
	return exists, nil
}

// CreateTeam creates one immutable team in a transaction.
func (s *Store) CreateTeam(ctx context.Context, req platform.CreateTeamRequest, ident platform.AdminIdentity) (platform.Team, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.Team{}, fmt.Errorf("begin create team: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var team platform.Team
	err = tx.QueryRowContext(ctx, `
		INSERT INTO teams (team_id, team_name, default_image)
		VALUES ($1, $2, $3)
		RETURNING team_id, team_name, default_image, ingested_at, archived_at`,
		uuid.NewString(), req.TeamName, req.DefaultImage,
	).Scan(&team.TeamID, &team.TeamName, &team.DefaultImage, &team.IngestedAt, &team.ArchivedAt)
	if err != nil {
		return platform.Team{}, classifyTeamError(err)
	}
	s.waitBarrier(ctx, "team_name_write_after_constraint_before_commit")
	if err := appendTeamAudit(ctx, tx, team, nil, "team.create", ident); err != nil {
		return platform.Team{}, err
	}
	if err := tx.Commit(); err != nil {
		return platform.Team{}, fmt.Errorf("commit create team: %w", err)
	}
	return team, nil
}

func (s *Store) GetTeam(ctx context.Context, teamID string) (platform.Team, error) {
	return scanTeam(s.db.QueryRowContext(ctx, `SELECT team_id, team_name, default_image, ingested_at, archived_at FROM teams WHERE team_id=$1`, teamID))
}

func (s *Store) UpdateTeam(ctx context.Context, teamID string, req platform.UpdateTeamRequest, ident platform.AdminIdentity) (platform.Team, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.Team{}, fmt.Errorf("begin update team: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	current, err := scanTeam(tx.QueryRowContext(ctx, `SELECT team_id, team_name, default_image, ingested_at, archived_at FROM teams WHERE team_id=$1 FOR UPDATE`, teamID))
	if err != nil {
		return platform.Team{}, err
	}
	if req.DefaultImage != nil {
		s.waitBarrier(ctx, "team_default_image_update_after_lock")
	}
	team, err := scanTeam(tx.QueryRowContext(ctx, `
		UPDATE teams SET
		  team_name=COALESCE($2, team_name),
		  default_image=COALESCE($3, default_image),
		  updated_at=now()
		WHERE team_id=$1
		RETURNING team_id, team_name, default_image, ingested_at, archived_at`, teamID, req.TeamName, req.DefaultImage))
	if err != nil {
		if errors.Is(err, ErrTeamNotFound) {
			return platform.Team{}, err
		}
		return platform.Team{}, classifyTeamError(err)
	}
	if req.TeamName != nil {
		s.waitBarrier(ctx, "team_name_write_after_constraint_before_commit")
	}
	if err := appendTeamAudit(ctx, tx, team, &current, "team.update", ident); err != nil {
		return platform.Team{}, err
	}
	if err := tx.Commit(); err != nil {
		return platform.Team{}, fmt.Errorf("commit update team: %w", err)
	}
	return team, nil
}

func (s *Store) ArchiveTeam(ctx context.Context, teamID string, ident platform.AdminIdentity) (platform.Team, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.Team{}, false, fmt.Errorf("begin archive team: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	current, err := scanTeam(tx.QueryRowContext(ctx, `SELECT team_id, team_name, default_image, ingested_at, archived_at FROM teams WHERE team_id=$1 FOR UPDATE`, teamID))
	if err != nil {
		return platform.Team{}, false, err
	}
	s.waitBarrier(ctx, "team_archive_after_lock")
	if current.ArchivedAt != nil {
		if err := tx.Commit(); err != nil {
			return platform.Team{}, false, fmt.Errorf("commit archive read: %w", err)
		}
		return current, false, nil
	}
	team, err := scanTeam(tx.QueryRowContext(ctx, `UPDATE teams SET archived_at=now(), updated_at=now() WHERE team_id=$1 RETURNING team_id, team_name, default_image, ingested_at, archived_at`, teamID))
	if err != nil {
		return platform.Team{}, false, err
	}
	if err := appendTeamAudit(ctx, tx, team, &current, "team.archive", ident); err != nil {
		return platform.Team{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return platform.Team{}, false, fmt.Errorf("commit archive team: %w", err)
	}
	return team, true, nil
}

func appendTeamAudit(ctx context.Context, tx *sql.Tx, team platform.Team, old *platform.Team, action string, ident platform.AdminIdentity) error {
	actor, requestID := strings.TrimSpace(ident.Subject), strings.TrimSpace(ident.RequestID)
	if actor == "" {
		actor = "system-administrator"
	}
	if requestID == "" {
		requestID = "request-unknown"
	}
	auditID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_entries (audit_id, team_id, actor_id, actor_type, action, resource_type, resource_id, request_id, outcome) VALUES ($1,$2,$3,'system_administrator',$4,'team',$2,$5,'succeeded')`, auditID, team.TeamID, actor, action, requestID); err != nil {
		return fmt.Errorf("append team audit: %w", err)
	}
	var oldName, oldImage any
	if old != nil {
		oldName, oldImage = old.TeamName, old.DefaultImage
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO team_audit_details (audit_id, old_team_name, new_team_name, old_default_image, new_default_image, archived_at) VALUES ($1,$2,$3,$4,$5,$6)`, auditID, oldName, team.TeamName, oldImage, team.DefaultImage, team.ArchivedAt); err != nil {
		return fmt.Errorf("append team audit details: %w", err)
	}
	return nil
}

func scanTeam(row interface{ Scan(...any) error }) (platform.Team, error) {
	var team platform.Team
	err := row.Scan(&team.TeamID, &team.TeamName, &team.DefaultImage, &team.IngestedAt, &team.ArchivedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return platform.Team{}, ErrTeamNotFound
	}
	if err != nil {
		return platform.Team{}, fmt.Errorf("read team: %w", err)
	}
	return team, nil
}

// CreateSourceSystem creates one immutable team-owned source system in a
// transaction.
func (s *Store) CreateSourceSystem(ctx context.Context, req platform.CreateSourceSystemRequest, _ platform.AdminIdentity) (platform.SourceSystem, error) {
	image, err := nullableImage(req.DefaultImage)
	if err != nil {
		return platform.SourceSystem{}, fmt.Errorf("marshal source-system default image: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.SourceSystem{}, fmt.Errorf("begin create source system: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var source platform.SourceSystem
	var storedImage []byte
	err = tx.QueryRowContext(ctx, `
		INSERT INTO source_systems (source_system_id, team_id, listener_identity, default_image)
		VALUES ($1, $2, $3, $4::jsonb)
		RETURNING source_system_id, team_id, listener_identity, default_image, created_at, updated_at`,
		uuid.NewString(), req.TeamID, req.ListenerIdentity, image,
	).Scan(
		&source.SourceSystemID,
		&source.TeamID,
		&source.ListenerIdentity,
		&storedImage,
		&source.CreatedAt,
		&source.UpdatedAt,
	)
	if err != nil {
		return platform.SourceSystem{}, classifySourceSystemError(err)
	}
	if err := decodeNullableImage(storedImage, &source.DefaultImage); err != nil {
		return platform.SourceSystem{}, fmt.Errorf("decode source-system default image: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return platform.SourceSystem{}, fmt.Errorf("commit create source system: %w", err)
	}
	return source, nil
}

// CreateTaskType creates one immutable team-owned task type in a transaction.
func (s *Store) CreateTaskType(ctx context.Context, req platform.CreateTaskTypeRequest, _ platform.AdminIdentity) (platform.TaskType, error) {
	image, err := nullableImage(req.DefaultImage)
	if err != nil {
		return platform.TaskType{}, fmt.Errorf("marshal task-type default image: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.TaskType{}, fmt.Errorf("begin create task type: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var taskType platform.TaskType
	var storedImage []byte
	err = tx.QueryRowContext(ctx, `
		INSERT INTO task_types (task_type_id, team_id, execution_tag, default_image)
		VALUES ($1, $2, $3, $4::jsonb)
		RETURNING task_type_id, team_id, execution_tag, default_image, created_at, updated_at`,
		uuid.NewString(), req.TeamID, req.ExecutionTag, image,
	).Scan(
		&taskType.TaskTypeID,
		&taskType.TeamID,
		&taskType.ExecutionTag,
		&storedImage,
		&taskType.CreatedAt,
		&taskType.UpdatedAt,
	)
	if err != nil {
		return platform.TaskType{}, classifyTaskTypeError(err)
	}
	if err := decodeNullableImage(storedImage, &taskType.DefaultImage); err != nil {
		return platform.TaskType{}, fmt.Errorf("decode task-type default image: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return platform.TaskType{}, fmt.Errorf("commit create task type: %w", err)
	}
	return taskType, nil
}

func nullableImage(image *platform.ImageReference) (any, error) {
	if image == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(image)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func decodeNullableImage(encoded []byte, dst **platform.ImageReference) error {
	if len(encoded) == 0 {
		*dst = nil
		return nil
	}
	var image platform.ImageReference
	if err := json.Unmarshal(encoded, &image); err != nil {
		return err
	}
	*dst = &image
	return nil
}

func classifyTeamError(err error) error {
	if constraintIs(err, "23505", "teams_team_name_key") {
		return ErrTeamNameConflict
	}
	return fmt.Errorf("persist team: %w", err)
}

func classifySourceSystemError(err error) error {
	if constraintIs(err, "23505", "source_systems_listener_identity_key") {
		return ErrListenerIdentityConflict
	}
	if sqlStateIs(err, "23503") {
		return ErrTeamNotFound
	}
	return fmt.Errorf("create source system: %w", err)
}

func classifyTaskTypeError(err error) error {
	if sqlStateIs(err, "23503") {
		return ErrTeamNotFound
	}
	return fmt.Errorf("create task type: %w", err)
}

func constraintIs(err error, code, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code && pgErr.ConstraintName == constraint
}

func sqlStateIs(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

var _ AdminRepository = (*Store)(nil)
var _ TeamLifecycleRepository = (*Store)(nil)
