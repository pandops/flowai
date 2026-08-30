// Package store persists API Gateway-owned authentication state only.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

var ErrMappingConflict = errors.New("mapping conflict")

type Mapping struct {
	Issuer     string
	OIDCTeamID string
	TeamID     string
	TeamName   *string
	ArchivedAt *time.Time
}

type MappingStore struct{ db *sql.DB }

func NewMappingStore(db *sql.DB) (*MappingStore, error) {
	if db == nil {
		return nil, errors.New("mapping store: db is nil")
	}
	return &MappingStore{db: db}, nil
}

// Register creates a mapping, returns the existing representation for an
// identical retry, and maps both one-to-one uniqueness races to ErrMappingConflict.
func (s *MappingStore) Register(ctx context.Context, mapping Mapping) (Mapping, bool, error) {
	if mapping.Issuer == "" || mapping.OIDCTeamID == "" || mapping.TeamID == "" {
		return Mapping{}, false, errors.New("mapping fields must be non-empty")
	}
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO api_gateway.oidc_team_mappings
			(issuer, oidc_team_id, team_id, team_name, archived_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (issuer, oidc_team_id) DO NOTHING
		RETURNING issuer, oidc_team_id, team_id, team_name, archived_at`,
		mapping.Issuer, mapping.OIDCTeamID, mapping.TeamID, mapping.TeamName, mapping.ArchivedAt,
	)
	created, err := scanMapping(row)
	if err == nil {
		return created, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		if uniqueViolation(err) {
			return Mapping{}, false, ErrMappingConflict
		}
		return Mapping{}, false, fmt.Errorf("insert mapping: %w", err)
	}

	existing, err := scanMapping(s.db.QueryRowContext(ctx, `
		SELECT issuer, oidc_team_id, team_id, team_name, archived_at
		FROM api_gateway.oidc_team_mappings
		WHERE issuer = $1 AND oidc_team_id = $2`, mapping.Issuer, mapping.OIDCTeamID))
	if err != nil {
		return Mapping{}, false, fmt.Errorf("read conflicting mapping: %w", err)
	}
	if existing.TeamID != mapping.TeamID {
		return Mapping{}, false, ErrMappingConflict
	}
	return existing, false, nil
}

func (s *MappingStore) Resolve(ctx context.Context, issuer string, oidcTeamIDs []string) ([]Mapping, error) {
	if len(oidcTeamIDs) == 0 {
		return []Mapping{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT issuer, oidc_team_id, team_id, team_name, archived_at
		FROM api_gateway.oidc_team_mappings
		WHERE issuer = $1 AND oidc_team_id = ANY($2)
		ORDER BY team_name ASC NULLS LAST, team_id ASC`, issuer, oidcTeamIDs)
	if err != nil {
		return nil, fmt.Errorf("resolve mappings: %w", err)
	}
	defer rows.Close()
	result := make([]Mapping, 0, len(oidcTeamIDs))
	for rows.Next() {
		mapping, err := scanMapping(rows)
		if err != nil {
			return nil, fmt.Errorf("scan mapping: %w", err)
		}
		result = append(result, mapping)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mappings: %w", err)
	}
	return result, nil
}

func (s *MappingStore) RefreshPresentation(ctx context.Context, teamID, teamName string, archivedAt *time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE api_gateway.oidc_team_mappings SET team_name=$2, archived_at=$3, updated_at=now() WHERE team_id=$1`, teamID, teamName, archivedAt)
	if err != nil {
		return fmt.Errorf("refresh mapping presentation: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return ErrMappingConflict
	}
	return nil
}

type scanner interface{ Scan(...any) error }

func scanMapping(row scanner) (Mapping, error) {
	var mapping Mapping
	err := row.Scan(&mapping.Issuer, &mapping.OIDCTeamID, &mapping.TeamID, &mapping.TeamName, &mapping.ArchivedAt)
	return mapping, err
}

func uniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
