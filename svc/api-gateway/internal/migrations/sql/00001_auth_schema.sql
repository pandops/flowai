-- +goose Up

CREATE SCHEMA IF NOT EXISTS api_gateway;

CREATE TABLE api_gateway.oidc_team_mappings (
    issuer text NOT NULL,
    oidc_team_id text NOT NULL CHECK (oidc_team_id <> ''),
    team_id text NOT NULL CHECK (team_id <> ''),
    team_name text,
    archived_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT oidc_team_mappings_external_unique UNIQUE (issuer, oidc_team_id),
    CONSTRAINT oidc_team_mappings_team_unique UNIQUE (team_id)
);

CREATE TABLE api_gateway.oidc_sessions (
    session_id_hash bytea PRIMARY KEY,
    operator_id text NOT NULL CHECK (operator_id <> ''),
    issuer text NOT NULL,
    subject text NOT NULL CHECK (subject <> ''),
    encrypted_provider_state bytea NOT NULL,
    membership_observed_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at > created_at)
);

CREATE TABLE api_gateway.membership_observations (
    session_id_hash bytea NOT NULL REFERENCES api_gateway.oidc_sessions(session_id_hash) ON DELETE CASCADE,
    oidc_team_id text NOT NULL CHECK (oidc_team_id <> ''),
    observed_at timestamptz NOT NULL,
    PRIMARY KEY (session_id_hash, oidc_team_id)
);

CREATE TABLE api_gateway.derived_operator_team_access (
    session_id_hash bytea NOT NULL REFERENCES api_gateway.oidc_sessions(session_id_hash) ON DELETE CASCADE,
    operator_id text NOT NULL CHECK (operator_id <> ''),
    team_id text NOT NULL CHECK (team_id <> ''),
    mapping_issuer text NOT NULL,
    mapping_oidc_team_id text NOT NULL,
    observed_at timestamptz NOT NULL,
    PRIMARY KEY (session_id_hash, team_id),
    FOREIGN KEY (mapping_issuer, mapping_oidc_team_id)
        REFERENCES api_gateway.oidc_team_mappings(issuer, oidc_team_id)
);

CREATE TABLE api_gateway.working_token_records (
    jti_hash bytea PRIMARY KEY,
    session_id_hash bytea NOT NULL REFERENCES api_gateway.oidc_sessions(session_id_hash) ON DELETE CASCADE,
    operator_id text NOT NULL,
    team_id text NOT NULL,
    issued_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    CHECK (expires_at > issued_at)
);

CREATE TABLE api_gateway.auth_audit (
    audit_id text PRIMARY KEY,
    action text NOT NULL,
    outcome text NOT NULL,
    operator_id text,
    team_id text,
    request_id text NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down

DROP TABLE api_gateway.auth_audit;
DROP TABLE api_gateway.working_token_records;
DROP TABLE api_gateway.derived_operator_team_access;
DROP TABLE api_gateway.membership_observations;
DROP TABLE api_gateway.oidc_sessions;
DROP TABLE api_gateway.oidc_team_mappings;
DROP SCHEMA api_gateway;
