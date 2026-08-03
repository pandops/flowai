package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/flowai/platform/state-registry/internal/platform"
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

// Store persists State Registry resources in PostgreSQL.
type Store struct {
	db                *sql.DB
	aesKey            []byte
	scopeTokenKeyring *scopeTokenKeyring
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
func (s *Store) CreateTeam(ctx context.Context, req platform.CreateTeamRequest, _ platform.AdminIdentity) (platform.Team, error) {
	image, err := json.Marshal(req.DefaultImage)
	if err != nil {
		return platform.Team{}, fmt.Errorf("marshal team default image: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.Team{}, fmt.Errorf("begin create team: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var team platform.Team
	var storedImage []byte
	err = tx.QueryRowContext(ctx, `
		INSERT INTO teams (team_id, team_name, default_image)
		VALUES ($1, $2, $3::jsonb)
		RETURNING team_id, team_name, default_image, created_at, updated_at`,
		uuid.NewString(), req.TeamName, image,
	).Scan(&team.TeamID, &team.TeamName, &storedImage, &team.CreatedAt, &team.UpdatedAt)
	if err != nil {
		return platform.Team{}, classifyTeamError(err)
	}
	if err := json.Unmarshal(storedImage, &team.DefaultImage); err != nil {
		return platform.Team{}, fmt.Errorf("decode team default image: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return platform.Team{}, fmt.Errorf("commit create team: %w", err)
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
	return fmt.Errorf("create team: %w", err)
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
