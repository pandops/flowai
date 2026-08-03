-- +goose Up
-- +goose StatementBegin

CREATE TABLE teams (
    team_id text PRIMARY KEY,
    team_name text NOT NULL UNIQUE CHECK (length(btrim(team_name)) > 0),
    default_image jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE source_systems (
    source_system_id text PRIMARY KEY,
    team_id text NOT NULL REFERENCES teams(team_id),
    listener_identity text NOT NULL UNIQUE CHECK (length(btrim(listener_identity)) > 0),
    default_image jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (team_id, source_system_id)
);

CREATE TABLE task_types (
    task_type_id text PRIMARY KEY,
    team_id text NOT NULL REFERENCES teams(team_id),
    execution_tag text NOT NULL CHECK (length(btrim(execution_tag)) > 0),
    default_image jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (team_id, task_type_id)
);

CREATE TABLE executors (
    executor_id text PRIMARY KEY,
    scope text NOT NULL CHECK (scope IN ('team', 'system')),
    team_id text REFERENCES teams(team_id),
    executor_type text NOT NULL CHECK (length(btrim(executor_type)) > 0),
    identity text NOT NULL CHECK (length(btrim(identity)) > 0),
    authorized_tag text NOT NULL CHECK (length(btrim(authorized_tag)) > 0),
    max_capacity integer NOT NULL CHECK (max_capacity >= 0),
    running_count integer NOT NULL CHECK (running_count >= 0),
    runtime_metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    registered_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (
        (scope = 'team' AND team_id IS NOT NULL)
        OR (scope = 'system' AND team_id IS NULL)
    ),
    UNIQUE (team_id, executor_id)
);

CREATE TABLE tasks (
    task_id text PRIMARY KEY,
    team_id text NOT NULL REFERENCES teams(team_id),
    source_system_id text NOT NULL,
    source_id text NOT NULL CHECK (length(btrim(source_id)) > 0),
    task_type_id text NOT NULL,
    required_tag text NOT NULL CHECK (length(btrim(required_tag)) > 0),
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    current_state text NOT NULL DEFAULT 'pending'
        CHECK (current_state IN ('pending', 'created', 'running', 'finished', 'failed')),
    owner_command_id text,
    executor_id text REFERENCES executors(executor_id),
    project_id text,
    environment_id text,
    image jsonb,
    resolved_image jsonb,
    image_source text CHECK (
        image_source IS NULL OR image_source IN (
            'task_override',
            'task_type_default',
            'source_system_default',
            'team_default'
        )
    ),
    ingested_at timestamptz NOT NULL DEFAULT now(),
    claimed_at timestamptz,
    UNIQUE (team_id, task_id),
    UNIQUE (team_id, source_system_id, source_id),
    FOREIGN KEY (team_id, source_system_id)
        REFERENCES source_systems(team_id, source_system_id),
    FOREIGN KEY (team_id, task_type_id)
        REFERENCES task_types(team_id, task_type_id),
    CHECK (
        (current_state = 'pending'
            AND owner_command_id IS NULL
            AND executor_id IS NULL
            AND resolved_image IS NULL
            AND image_source IS NULL
            AND claimed_at IS NULL)
        OR (current_state <> 'pending'
            AND owner_command_id IS NOT NULL
            AND executor_id IS NOT NULL
            AND resolved_image IS NOT NULL
            AND image_source IS NOT NULL
            AND claimed_at IS NOT NULL)
    )
);

CREATE TABLE task_events (
    event_id text NOT NULL,
    team_id text NOT NULL,
    task_id text NOT NULL,
    executor_id text NOT NULL REFERENCES executors(executor_id),
    event_type text NOT NULL CHECK (event_type IN ('created', 'running', 'finished', 'failed')),
    occurred_at timestamptz NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (event_id),
    UNIQUE (task_id, event_id),
    FOREIGN KEY (team_id, task_id) REFERENCES tasks(team_id, task_id)
);

CREATE TABLE executor_events (
    event_id text PRIMARY KEY,
    executor_id text NOT NULL REFERENCES executors(executor_id),
    team_id text REFERENCES teams(team_id),
    task_id text REFERENCES tasks(task_id),
    event_type text NOT NULL CHECK (event_type IN (
        'registered', 'started', 'healthy', 'busy', 'idle', 'stopping',
        'stopped', 'failed', 'capacity_observed', 'running_count_observed'
    )),
    occurred_at timestamptz NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    FOREIGN KEY (team_id, executor_id) REFERENCES executors(team_id, executor_id)
);

CREATE TABLE environment_definitions (
    environment_id text PRIMARY KEY,
    team_id text NOT NULL REFERENCES teams(team_id),
    task_id text,
    parent_task_id text,
    project_id text,
    name text NOT NULL CHECK (length(btrim(name)) > 0),
    values jsonb NOT NULL DEFAULT '{}'::jsonb,
    revision integer NOT NULL DEFAULT 1 CHECK (revision >= 1),
    deleted boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (team_id, environment_id),
    FOREIGN KEY (team_id, task_id) REFERENCES tasks(team_id, task_id),
    FOREIGN KEY (team_id, parent_task_id) REFERENCES tasks(team_id, task_id)
);

CREATE TABLE secrets (
    secret_id text PRIMARY KEY,
    team_id text NOT NULL REFERENCES teams(team_id),
    environment_id text NOT NULL,
    name text NOT NULL CHECK (length(btrim(name)) > 0),
    revoked boolean NOT NULL DEFAULT false,
    latest_version integer CHECK (latest_version IS NULL OR latest_version >= 1),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (team_id, secret_id),
    FOREIGN KEY (team_id, environment_id)
        REFERENCES environment_definitions(team_id, environment_id)
);

CREATE TABLE secret_versions (
    secret_id text NOT NULL,
    version integer NOT NULL CHECK (version >= 1),
    team_id text NOT NULL,
    ciphertext bytea NOT NULL,
    nonce bytea NOT NULL CHECK (octet_length(nonce) = 12),
    authentication_tag bytea NOT NULL CHECK (octet_length(authentication_tag) = 16),
    key_id text NOT NULL CHECK (length(btrim(key_id)) > 0),
    key_version integer NOT NULL CHECK (key_version >= 1),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (secret_id, version),
    FOREIGN KEY (team_id, secret_id) REFERENCES secrets(team_id, secret_id)
);

CREATE TABLE audit_entries (
    audit_id text PRIMARY KEY,
    team_id text REFERENCES teams(team_id),
    actor_id text NOT NULL CHECK (length(btrim(actor_id)) > 0),
    actor_type text NOT NULL CHECK (actor_type IN (
        'operator', 'executor', 'listener', 'system_administrator', 'state_registry'
    )),
    action text NOT NULL CHECK (length(btrim(action)) > 0),
    resource_type text NOT NULL CHECK (length(btrim(resource_type)) > 0),
    resource_id text NOT NULL CHECK (length(btrim(resource_id)) > 0),
    request_id text NOT NULL CHECK (length(btrim(request_id)) > 0),
    outcome text NOT NULL CHECK (outcome IN ('accepted', 'rejected', 'succeeded', 'failed')),
    executor_scope text CHECK (executor_scope IS NULL OR executor_scope IN ('team', 'system')),
    occurred_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE task_control_requests (
    control_id text PRIMARY KEY,
    team_id text NOT NULL,
    task_id text NOT NULL,
    operator_id text NOT NULL CHECK (length(btrim(operator_id)) > 0),
    action text NOT NULL CHECK (action IN ('cancel', 'interrupt')),
    idempotency_key text NOT NULL CHECK (length(btrim(idempotency_key)) > 0),
    reason text,
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'acknowledged', 'completed', 'failed')),
    audit_id text REFERENCES audit_entries(audit_id),
    requested_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (team_id, task_id) REFERENCES tasks(team_id, task_id),
    UNIQUE (team_id, task_id, operator_id, action, idempotency_key)
);

CREATE INDEX tasks_pending_discovery_idx
    ON tasks (current_state, required_tag, ingested_at, task_id);
CREATE INDEX tasks_system_discovery_idx
    ON tasks (required_tag, ingested_at, task_id)
    WHERE current_state = 'pending';
CREATE INDEX task_events_order_idx
    ON task_events (task_id, occurred_at, event_id);
CREATE INDEX executors_team_tag_idx
    ON executors (team_id, executor_id, authorized_tag);

CREATE FUNCTION reject_immutable_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% rows are immutable', TG_TABLE_NAME USING ERRCODE = '23000';
END;
$$;

CREATE FUNCTION protect_team_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.team_id IS DISTINCT FROM OLD.team_id
       OR NEW.default_image IS DISTINCT FROM OLD.default_image THEN
        RAISE EXCEPTION 'teams.team_id and teams.default_image are immutable' USING ERRCODE = '23000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION protect_task_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.task_id IS DISTINCT FROM OLD.task_id
       OR NEW.team_id IS DISTINCT FROM OLD.team_id
       OR NEW.source_system_id IS DISTINCT FROM OLD.source_system_id
       OR NEW.source_id IS DISTINCT FROM OLD.source_id
       OR NEW.task_type_id IS DISTINCT FROM OLD.task_type_id
       OR NEW.required_tag IS DISTINCT FROM OLD.required_tag
       OR NEW.image IS DISTINCT FROM OLD.image
       OR NEW.ingested_at IS DISTINCT FROM OLD.ingested_at THEN
        RAISE EXCEPTION 'task ingestion identity fields are immutable' USING ERRCODE = '23000';
    END IF;
    IF (OLD.owner_command_id IS NOT NULL AND NEW.owner_command_id IS DISTINCT FROM OLD.owner_command_id)
       OR (OLD.executor_id IS NOT NULL AND NEW.executor_id IS DISTINCT FROM OLD.executor_id)
       OR (OLD.resolved_image IS NOT NULL AND NEW.resolved_image IS DISTINCT FROM OLD.resolved_image)
       OR (OLD.image_source IS NOT NULL AND NEW.image_source IS DISTINCT FROM OLD.image_source)
       OR (OLD.claimed_at IS NOT NULL AND NEW.claimed_at IS DISTINCT FROM OLD.claimed_at) THEN
        RAISE EXCEPTION 'task claim fields are immutable once set' USING ERRCODE = '23000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION protect_team_owner_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.team_id IS DISTINCT FROM OLD.team_id THEN
        RAISE EXCEPTION '%.team_id is immutable', TG_TABLE_NAME USING ERRCODE = '23000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION validate_task_executor_ownership() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    executor_scope text;
    executor_team_id text;
BEGIN
    IF NEW.executor_id IS NULL THEN
        RETURN NEW;
    END IF;
    SELECT scope, team_id
      INTO executor_scope, executor_team_id
      FROM executors
     WHERE executor_id = NEW.executor_id;
    IF NOT FOUND THEN
        RETURN NEW;
    END IF;
    IF executor_scope = 'team' AND executor_team_id IS DISTINCT FROM NEW.team_id THEN
        RAISE EXCEPTION 'team-owned executor and task must share team_id' USING ERRCODE = '23503';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION validate_executor_event_scope() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    executor_scope text;
    executor_team_id text;
    task_team_id text;
BEGIN
    SELECT scope, team_id
      INTO executor_scope, executor_team_id
      FROM executors
     WHERE executor_id = NEW.executor_id;
    IF NOT FOUND THEN
        RETURN NEW;
    END IF;
    IF executor_scope = 'team' AND NEW.team_id IS DISTINCT FROM executor_team_id THEN
        RAISE EXCEPTION 'team-owned executor event must carry its executor team_id' USING ERRCODE = '23503';
    END IF;
    IF executor_scope = 'system' AND NEW.team_id IS NOT NULL THEN
        RAISE EXCEPTION 'system-owned executor event must carry a null team_id' USING ERRCODE = '23503';
    END IF;
    IF executor_scope = 'team' AND NEW.task_id IS NOT NULL THEN
        SELECT team_id INTO task_team_id FROM tasks WHERE task_id = NEW.task_id;
        IF FOUND AND task_team_id IS DISTINCT FROM executor_team_id THEN
            RAISE EXCEPTION 'team-owned executor event task must share executor team_id' USING ERRCODE = '23503';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION validate_task_event_scope() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    executor_scope text;
    executor_team_id text;
BEGIN
    SELECT scope, team_id
      INTO executor_scope, executor_team_id
      FROM executors
     WHERE executor_id = NEW.executor_id;
    IF FOUND AND executor_scope = 'team' AND NEW.team_id IS DISTINCT FROM executor_team_id THEN
        RAISE EXCEPTION 'team-owned task event must share executor team_id' USING ERRCODE = '23503';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER teams_immutable_fields
BEFORE UPDATE ON teams FOR EACH ROW EXECUTE FUNCTION protect_team_update();

CREATE TRIGGER tasks_immutable_fields
BEFORE UPDATE ON tasks FOR EACH ROW EXECUTE FUNCTION protect_task_update();

CREATE TRIGGER tasks_executor_ownership
BEFORE INSERT OR UPDATE OF executor_id, team_id ON tasks
FOR EACH ROW EXECUTE FUNCTION validate_task_executor_ownership();

CREATE TRIGGER source_systems_immutable_team
BEFORE UPDATE ON source_systems FOR EACH ROW EXECUTE FUNCTION protect_team_owner_update();
CREATE TRIGGER task_types_immutable_team
BEFORE UPDATE ON task_types FOR EACH ROW EXECUTE FUNCTION protect_team_owner_update();
CREATE TRIGGER executors_immutable_team
BEFORE UPDATE ON executors FOR EACH ROW EXECUTE FUNCTION protect_team_owner_update();
CREATE TRIGGER environment_definitions_immutable_team
BEFORE UPDATE ON environment_definitions FOR EACH ROW EXECUTE FUNCTION protect_team_owner_update();
CREATE TRIGGER secrets_immutable_team
BEFORE UPDATE ON secrets FOR EACH ROW EXECUTE FUNCTION protect_team_owner_update();
CREATE TRIGGER task_control_requests_immutable_team
BEFORE UPDATE ON task_control_requests FOR EACH ROW EXECUTE FUNCTION protect_team_owner_update();

CREATE TRIGGER task_events_append_only
BEFORE UPDATE OR DELETE ON task_events FOR EACH ROW EXECUTE FUNCTION reject_immutable_update();
CREATE TRIGGER task_events_scope
BEFORE INSERT ON task_events FOR EACH ROW EXECUTE FUNCTION validate_task_event_scope();
CREATE TRIGGER executor_events_append_only
BEFORE UPDATE OR DELETE ON executor_events FOR EACH ROW EXECUTE FUNCTION reject_immutable_update();
CREATE TRIGGER executor_events_scope
BEFORE INSERT ON executor_events FOR EACH ROW EXECUTE FUNCTION validate_executor_event_scope();
CREATE TRIGGER secret_versions_append_only
BEFORE UPDATE OR DELETE ON secret_versions FOR EACH ROW EXECUTE FUNCTION reject_immutable_update();
CREATE TRIGGER audit_entries_append_only
BEFORE UPDATE OR DELETE ON audit_entries FOR EACH ROW EXECUTE FUNCTION reject_immutable_update();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE task_control_requests;
DROP TABLE audit_entries;
DROP TABLE secret_versions;
DROP TABLE secrets;
DROP TABLE environment_definitions;
DROP TABLE executor_events;
DROP TABLE task_events;
DROP TABLE tasks;
DROP TABLE executors;
DROP TABLE task_types;
DROP TABLE source_systems;
DROP TABLE teams;

DROP FUNCTION protect_task_update();
DROP FUNCTION protect_team_update();
DROP FUNCTION protect_team_owner_update();
DROP FUNCTION validate_task_executor_ownership();
DROP FUNCTION validate_task_event_scope();
DROP FUNCTION validate_executor_event_scope();
DROP FUNCTION reject_immutable_update();

-- +goose StatementEnd
