-- +goose Up

ALTER TABLE environment_definitions
    ALTER COLUMN team_id DROP NOT NULL,
    ADD COLUMN scope_kind text,
    ADD COLUMN task_type_id text,
    ADD COLUMN image text;

ALTER TABLE environment_definitions
    DROP COLUMN parent_task_id,
    DROP COLUMN task_id,
    DROP COLUMN project_id;

ALTER TABLE secrets ALTER COLUMN team_id DROP NOT NULL;
ALTER TABLE secret_versions ALTER COLUMN team_id DROP NOT NULL;

ALTER TABLE tasks
    DROP COLUMN environment_id;

ALTER TABLE tasks
    DROP CONSTRAINT tasks_image_source_check,
    ADD CONSTRAINT tasks_image_source_check CHECK (
        image_source IS NULL OR image_source IN (
            'task_override',
            'task_type_launch_parameters',
            'team_launch_parameters',
            'global_launch_parameters',
            'task_type_default',
            'source_system_default',
            'team_default'
        )
    );

ALTER TABLE environment_definitions
    ADD CONSTRAINT environment_scope_kind_valid
        CHECK (scope_kind IS NULL OR scope_kind IN ('global', 'team', 'task_type')),
    ADD CONSTRAINT environment_task_type_scope_valid CHECK (
        scope_kind IS NULL OR
        (scope_kind = 'global' AND team_id IS NULL AND task_type_id IS NULL) OR
        (scope_kind = 'team' AND team_id IS NOT NULL AND task_type_id IS NULL) OR
        (scope_kind = 'task_type' AND team_id IS NOT NULL AND task_type_id IS NOT NULL)
    ),
    ADD CONSTRAINT environment_task_type_owner
        FOREIGN KEY (team_id, task_type_id) REFERENCES task_types(team_id, task_type_id);

CREATE UNIQUE INDEX environment_one_active_global_scope
    ON environment_definitions (scope_kind)
    WHERE scope_kind = 'global' AND deleted = false;

CREATE UNIQUE INDEX environment_one_active_team_scope
    ON environment_definitions (team_id, scope_kind)
    WHERE scope_kind = 'team' AND deleted = false;

CREATE UNIQUE INDEX environment_one_active_task_type_scope
    ON environment_definitions (team_id, task_type_id, scope_kind)
    WHERE scope_kind = 'task_type' AND deleted = false;

CREATE TABLE environment_revisions (
    environment_id text NOT NULL,
    revision integer NOT NULL CHECK (revision >= 1),
    team_id text,
    scope_kind text NOT NULL CHECK (scope_kind IN ('global', 'team', 'task_type')),
    task_type_id text,
    name text NOT NULL,
    values jsonb NOT NULL DEFAULT '{}'::jsonb,
    secret_keys jsonb NOT NULL DEFAULT '[]'::jsonb,
    image text,
    deleted boolean NOT NULL DEFAULT false,
    actor_id text NOT NULL,
    request_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (environment_id, revision),
    FOREIGN KEY (environment_id) REFERENCES environment_definitions(environment_id),
    FOREIGN KEY (team_id, task_type_id) REFERENCES task_types(team_id, task_type_id)
);

CREATE TABLE task_control_events (
    control_event_id text PRIMARY KEY,
    control_id text NOT NULL,
    task_id text NOT NULL,
    team_id text NOT NULL,
    executor_id text,
    status text NOT NULL CHECK (status IN ('pending', 'acknowledged', 'completed', 'failed')),
    occurred_at timestamptz NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    UNIQUE (control_id, occurred_at, control_event_id),
    FOREIGN KEY (team_id, task_id) REFERENCES tasks(team_id, task_id),
    FOREIGN KEY (control_id) REFERENCES task_control_requests(control_id),
    FOREIGN KEY (executor_id) REFERENCES executors(executor_id)
);

CREATE INDEX task_control_events_order
    ON task_control_events (team_id, task_id, occurred_at, control_event_id);

CREATE TABLE task_log_chunks (
    log_chunk_id text PRIMARY KEY,
    task_id text NOT NULL,
    team_id text NOT NULL,
    executor_id text NOT NULL,
    log_offset bigint NOT NULL CHECK (log_offset >= 1),
    stream text NOT NULL CHECK (stream IN ('work', 'reasoning')),
    content text NOT NULL,
    occurred_at timestamptz NOT NULL,
    UNIQUE (task_id, log_offset),
    FOREIGN KEY (team_id, task_id) REFERENCES tasks(team_id, task_id),
    FOREIGN KEY (executor_id) REFERENCES executors(executor_id)
);

CREATE INDEX task_log_chunks_replay
    ON task_log_chunks (team_id, task_id, log_offset);

CREATE TABLE task_launch_parameter_snapshots (
    task_id text PRIMARY KEY,
    team_id text NOT NULL,
    values jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (team_id, task_id),
    FOREIGN KEY (team_id, task_id) REFERENCES tasks(team_id, task_id)
);

CREATE TABLE task_launch_parameter_secret_refs (
    task_id text NOT NULL,
    team_id text NOT NULL,
    key text NOT NULL CHECK (key ~ '^[A-Z_][A-Z0-9_]*$'),
    secret_id text NOT NULL,
    version integer NOT NULL CHECK (version >= 1),
    PRIMARY KEY (task_id, key),
    FOREIGN KEY (team_id, task_id)
        REFERENCES task_launch_parameter_snapshots(team_id, task_id),
    FOREIGN KEY (secret_id, version)
        REFERENCES secret_versions(secret_id, version)
);

-- +goose Down

DROP TABLE task_launch_parameter_secret_refs;
DROP TABLE task_launch_parameter_snapshots;
DROP TABLE task_log_chunks;
DROP TABLE task_control_events;
DROP TABLE environment_revisions;
DROP INDEX environment_one_active_task_type_scope;
DROP INDEX environment_one_active_team_scope;
DROP INDEX environment_one_active_global_scope;
ALTER TABLE tasks
    ADD COLUMN environment_id text;
ALTER TABLE tasks
    DROP CONSTRAINT tasks_image_source_check,
    ADD CONSTRAINT tasks_image_source_check CHECK (
        image_source IS NULL OR image_source IN (
            'task_override',
            'task_type_default',
            'source_system_default',
            'team_default'
        )
    );
ALTER TABLE environment_definitions
    DROP CONSTRAINT environment_task_type_owner,
    DROP CONSTRAINT environment_task_type_scope_valid,
    DROP CONSTRAINT environment_scope_kind_valid,
    DROP COLUMN image,
    DROP COLUMN task_type_id,
    DROP COLUMN scope_kind,
    ADD COLUMN project_id text,
    ADD COLUMN task_id text,
    ADD COLUMN parent_task_id text,
    ALTER COLUMN team_id SET NOT NULL;

ALTER TABLE environment_definitions
    ADD FOREIGN KEY (team_id, task_id) REFERENCES tasks(team_id, task_id),
    ADD FOREIGN KEY (team_id, parent_task_id) REFERENCES tasks(team_id, task_id);
ALTER TABLE secret_versions ALTER COLUMN team_id SET NOT NULL;
ALTER TABLE secrets ALTER COLUMN team_id SET NOT NULL;
