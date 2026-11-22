-- +goose Up
CREATE TABLE audit_log (
    id uuid NOT NULL DEFAULT uuidv7(),
    actor_sub uuid,
    action text NOT NULL,
    resource_type text NOT NULL,
    resource_id text NOT NULL DEFAULT '',
    request_id text NOT NULL DEFAULT '',
    ip text NOT NULL DEFAULT '',
    data jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT audit_log_pkey PRIMARY KEY (id)
);

CREATE INDEX audit_log_created_at_idx ON audit_log (created_at, id);
CREATE INDEX audit_log_actor_sub_idx ON audit_log (actor_sub);

-- +goose Down
DROP TABLE audit_log;
