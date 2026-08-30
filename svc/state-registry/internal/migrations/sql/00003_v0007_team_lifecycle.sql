-- +goose Up
-- +goose StatementBegin

ALTER TABLE teams
    ADD COLUMN ingested_at timestamptz,
    ADD COLUMN archived_at timestamptz;

UPDATE teams SET ingested_at = created_at;
ALTER TABLE teams ALTER COLUMN ingested_at SET NOT NULL;
ALTER TABLE teams ALTER COLUMN ingested_at SET DEFAULT now();
ALTER TABLE teams ALTER COLUMN default_image TYPE text
    USING (default_image->>'repository') || '@' || (default_image->>'digest');

CREATE TABLE team_audit_details (
    audit_id text PRIMARY KEY REFERENCES audit_entries(audit_id),
    old_team_name text,
    new_team_name text NOT NULL,
    old_default_image text,
    new_default_image text NOT NULL,
    archived_at timestamptz
);
CREATE TRIGGER team_audit_details_append_only
BEFORE UPDATE OR DELETE ON team_audit_details FOR EACH ROW EXECUTE FUNCTION reject_immutable_update();

CREATE OR REPLACE FUNCTION protect_team_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.team_id IS DISTINCT FROM OLD.team_id
       OR NEW.ingested_at IS DISTINCT FROM OLD.ingested_at
       OR (OLD.archived_at IS NOT NULL AND NEW.archived_at IS DISTINCT FROM OLD.archived_at) THEN
        RAISE EXCEPTION 'teams.team_id, teams.ingested_at, and an established teams.archived_at are immutable' USING ERRCODE = '23000';
    END IF;
    RETURN NEW;
END;
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE OR REPLACE FUNCTION protect_team_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.team_id IS DISTINCT FROM OLD.team_id
       OR NEW.default_image IS DISTINCT FROM OLD.default_image THEN
        RAISE EXCEPTION 'teams.team_id and teams.default_image are immutable' USING ERRCODE = '23000';
    END IF;
    RETURN NEW;
END;
$$;

DROP TABLE team_audit_details;
ALTER TABLE teams ALTER COLUMN default_image TYPE jsonb
    USING jsonb_build_object(
        'repository', split_part(default_image, '@', 1),
        'digest', substring(default_image from position('@' in default_image) + 1)
    );
ALTER TABLE teams DROP COLUMN archived_at, DROP COLUMN ingested_at;

-- +goose StatementEnd
