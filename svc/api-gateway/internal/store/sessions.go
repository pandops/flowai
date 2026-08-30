package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var (
	ErrSessionNotFound   = errors.New("session not found")
	ErrTeamNotAccessible = errors.New("team not accessible")
)

type SessionInput struct {
	SessionID              string
	OperatorID             string
	Issuer                 string
	Subject                string
	EncryptedProviderState []byte
	OIDCTeamIDs            []string
	ObservedAt             time.Time
	ExpiresAt              time.Time
}

type AccessibleTeam struct {
	TeamID     string
	TeamName   *string
	ArchivedAt *time.Time
}
type TokenContext struct {
	SessionHash [32]byte
	OperatorID  string
	TeamID      string
	TeamName    *string
}
type MembershipState struct {
	EncryptedProviderState []byte
	Issuer                 string
	Subject                string
	ObservedAt             time.Time
}

type SessionStore struct{ db *sql.DB }

func NewSessionStore(db *sql.DB) (*SessionStore, error) {
	if db == nil {
		return nil, errors.New("session store: db is nil")
	}
	return &SessionStore{db: db}, nil
}

func (s *SessionStore) Create(ctx context.Context, input SessionInput) error {
	if input.SessionID == "" || input.OperatorID == "" || input.Issuer == "" || input.Subject == "" || len(input.EncryptedProviderState) == 0 || !input.ExpiresAt.After(input.ObservedAt) {
		return errors.New("invalid session input")
	}
	hash := sessionHash(input.SessionID)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin session: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO api_gateway.oidc_sessions (session_id_hash, operator_id, issuer, subject, encrypted_provider_state, membership_observed_at, expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`, hash[:], input.OperatorID, input.Issuer, input.Subject, input.EncryptedProviderState, input.ObservedAt, input.ExpiresAt)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	for _, oidcTeamID := range input.OIDCTeamIDs {
		if oidcTeamID == "" {
			return errors.New("empty OIDC team ID")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO api_gateway.membership_observations (session_id_hash, oidc_team_id, observed_at) VALUES ($1,$2,$3)`, hash[:], oidcTeamID, input.ObservedAt); err != nil {
			return fmt.Errorf("insert membership: %w", err)
		}
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO api_gateway.derived_operator_team_access (session_id_hash, operator_id, team_id, mapping_issuer, mapping_oidc_team_id, observed_at)
		SELECT $1, $2, mapping.team_id, mapping.issuer, mapping.oidc_team_id, $3
		FROM api_gateway.oidc_team_mappings mapping
		JOIN api_gateway.membership_observations observation ON observation.session_id_hash=$1 AND observation.oidc_team_id=mapping.oidc_team_id
		WHERE mapping.issuer=$4`, hash[:], input.OperatorID, input.ObservedAt, input.Issuer)
	if err != nil {
		return fmt.Errorf("derive access: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit session: %w", err)
	}
	return nil
}

func (s *SessionStore) AccessibleTeams(ctx context.Context, sessionID string, now time.Time) ([]AccessibleTeam, error) {
	hash := sessionHash(sessionID)
	rows, err := s.db.QueryContext(ctx, `
		SELECT mapping.team_id, mapping.team_name, mapping.archived_at
		FROM api_gateway.oidc_sessions session
		JOIN api_gateway.derived_operator_team_access access ON access.session_id_hash=session.session_id_hash
		JOIN api_gateway.oidc_team_mappings mapping ON mapping.issuer=access.mapping_issuer AND mapping.oidc_team_id=access.mapping_oidc_team_id
		WHERE session.session_id_hash=$1 AND session.expires_at>$2
		ORDER BY mapping.team_name ASC NULLS LAST, mapping.team_id ASC`, hash[:], now)
	if err != nil {
		return nil, fmt.Errorf("list accessible teams: %w", err)
	}
	defer rows.Close()
	teams := []AccessibleTeam{}
	for rows.Next() {
		var team AccessibleTeam
		if err := rows.Scan(&team.TeamID, &team.TeamName, &team.ArchivedAt); err != nil {
			return nil, fmt.Errorf("scan accessible team: %w", err)
		}
		teams = append(teams, team)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate accessible teams: %w", err)
	}
	if len(teams) == 0 {
		var exists bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM api_gateway.oidc_sessions WHERE session_id_hash=$1 AND expires_at>$2)`, hash[:], now).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, ErrSessionNotFound
		}
	}
	return teams, nil
}

func (s *SessionStore) Delete(ctx context.Context, sessionID string) error {
	hash := sessionHash(sessionID)
	result, err := s.db.ExecContext(ctx, `DELETE FROM api_gateway.oidc_sessions WHERE session_id_hash=$1`, hash[:])
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrSessionNotFound
	}
	return nil
}

func (s *SessionStore) TokenContext(ctx context.Context, sessionID, teamID string, now time.Time) (TokenContext, error) {
	hash := sessionHash(sessionID)
	result := TokenContext{SessionHash: hash}
	err := s.db.QueryRowContext(ctx, `SELECT session.operator_id, mapping.team_id, mapping.team_name FROM api_gateway.oidc_sessions session JOIN api_gateway.derived_operator_team_access access ON access.session_id_hash=session.session_id_hash JOIN api_gateway.oidc_team_mappings mapping ON mapping.issuer=access.mapping_issuer AND mapping.oidc_team_id=access.mapping_oidc_team_id WHERE session.session_id_hash=$1 AND session.expires_at>$2 AND mapping.team_id=$3`, hash[:], now, teamID).Scan(&result.OperatorID, &result.TeamID, &result.TeamName)
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if checkErr := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM api_gateway.oidc_sessions WHERE session_id_hash=$1 AND expires_at>$2)`, hash[:], now).Scan(&exists); checkErr != nil {
			return TokenContext{}, checkErr
		}
		if exists {
			return TokenContext{}, ErrTeamNotAccessible
		}
		return TokenContext{}, ErrSessionNotFound
	}
	if err != nil {
		return TokenContext{}, fmt.Errorf("read token context: %w", err)
	}
	return result, nil
}

func (s *SessionStore) MembershipState(ctx context.Context, sessionID string, now time.Time) (MembershipState, error) {
	hash := sessionHash(sessionID)
	var state MembershipState
	err := s.db.QueryRowContext(ctx, `SELECT encrypted_provider_state, issuer, subject, membership_observed_at FROM api_gateway.oidc_sessions WHERE session_id_hash=$1 AND expires_at>$2`, hash[:], now).Scan(&state.EncryptedProviderState, &state.Issuer, &state.Subject, &state.ObservedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return MembershipState{}, ErrSessionNotFound
	}
	if err != nil {
		return MembershipState{}, fmt.Errorf("read membership state: %w", err)
	}
	return state, nil
}

func (s *SessionStore) ReplaceMembership(ctx context.Context, sessionID string, encrypted []byte, oidcTeamIDs []string, observedAt time.Time) error {
	hash := sessionHash(sessionID)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var operatorID, issuer string
	if err := tx.QueryRowContext(ctx, `SELECT operator_id,issuer FROM api_gateway.oidc_sessions WHERE session_id_hash=$1 FOR UPDATE`, hash[:]).Scan(&operatorID, &issuer); errors.Is(err, sql.ErrNoRows) {
		return ErrSessionNotFound
	} else if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE api_gateway.oidc_sessions SET encrypted_provider_state=$2,membership_observed_at=$3 WHERE session_id_hash=$1`, hash[:], encrypted, observedAt); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM api_gateway.membership_observations WHERE session_id_hash=$1`, hash[:]); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM api_gateway.derived_operator_team_access WHERE session_id_hash=$1`, hash[:]); err != nil {
		return err
	}
	for _, id := range oidcTeamIDs {
		if id == "" {
			return errors.New("empty OIDC team ID")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO api_gateway.membership_observations(session_id_hash,oidc_team_id,observed_at)VALUES($1,$2,$3)`, hash[:], id, observedAt); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO api_gateway.derived_operator_team_access(session_id_hash,operator_id,team_id,mapping_issuer,mapping_oidc_team_id,observed_at) SELECT $1,$2,mapping.team_id,mapping.issuer,mapping.oidc_team_id,$3 FROM api_gateway.oidc_team_mappings mapping JOIN api_gateway.membership_observations observation ON observation.session_id_hash=$1 AND observation.oidc_team_id=mapping.oidc_team_id WHERE mapping.issuer=$4`, hash[:], operatorID, observedAt, issuer); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SessionStore) RecordToken(ctx context.Context, context TokenContext, jti string, issuedAt, expiresAt time.Time) error {
	hash := sha256.Sum256([]byte(jti))
	_, err := s.db.ExecContext(ctx, `INSERT INTO api_gateway.working_token_records (jti_hash, session_id_hash, operator_id, team_id, issued_at, expires_at) VALUES ($1,$2,$3,$4,$5,$6)`, hash[:], context.SessionHash[:], context.OperatorID, context.TeamID, issuedAt, expiresAt)
	return err
}

func (s *SessionStore) ValidateTokenAccess(ctx context.Context, operatorID, teamID, jti string, now time.Time) error {
	jtiHash := sha256.Sum256([]byte(jti))
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM api_gateway.working_token_records token JOIN api_gateway.oidc_sessions session ON session.session_id_hash=token.session_id_hash JOIN api_gateway.derived_operator_team_access access ON access.session_id_hash=session.session_id_hash AND access.team_id=token.team_id WHERE token.jti_hash=$1 AND token.operator_id=$2 AND token.team_id=$3 AND token.expires_at>$4 AND session.expires_at>$4)`, jtiHash[:], operatorID, teamID, now).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return ErrTeamNotAccessible
	}
	return nil
}
func sessionHash(sessionID string) [32]byte { return sha256.Sum256([]byte(sessionID)) }
