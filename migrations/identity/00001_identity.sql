-- +goose Up
CREATE TABLE app_user (
    id                uuid        NOT NULL DEFAULT uuidv7(),
    email             text,
    phone             text,
    password_hash     text,
    display_name      text        NOT NULL DEFAULT '',
    avatar_file_id    uuid,
    status            text        NOT NULL DEFAULT 'active',
    email_verified_at timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    deleted_at        timestamptz,
    CONSTRAINT app_user_pkey PRIMARY KEY (id),
    CONSTRAINT app_user_status_check CHECK (status IN ('active', 'disabled')),
    CONSTRAINT app_user_email_check CHECK (email = lower(btrim(email)))
);

CREATE UNIQUE INDEX app_user_email_key ON app_user (email) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX app_user_phone_key ON app_user (phone) WHERE deleted_at IS NULL;
CREATE INDEX app_user_created_at_idx ON app_user (created_at);

CREATE TABLE user_role (
    user_id   uuid NOT NULL,
    role_code text NOT NULL,
    CONSTRAINT user_role_pkey PRIMARY KEY (user_id, role_code),
    CONSTRAINT user_role_user_id_fkey FOREIGN KEY (user_id) REFERENCES app_user (id) ON DELETE CASCADE
);

CREATE INDEX user_role_role_code_idx ON user_role (role_code);

CREATE TABLE session (
    id             uuid        NOT NULL DEFAULT uuidv7(),
    user_id        uuid        NOT NULL,
    refresh_hash   bytea       NOT NULL,
    auth_time      timestamptz NOT NULL,
    expires_at     timestamptz NOT NULL,
    revoked_at     timestamptz,
    user_agent     text        NOT NULL DEFAULT '',
    ip             text        NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now(),
    last_active_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT session_pkey PRIMARY KEY (id),
    CONSTRAINT session_refresh_hash_key UNIQUE (refresh_hash),
    CONSTRAINT session_user_id_fkey FOREIGN KEY (user_id) REFERENCES app_user (id) ON DELETE CASCADE
);

CREATE INDEX session_user_id_idx ON session (user_id);
CREATE INDEX session_expires_at_idx ON session (expires_at);

CREATE TABLE session_refresh_history (
    refresh_hash bytea       NOT NULL,
    session_id   uuid        NOT NULL,
    rotated_at   timestamptz NOT NULL,
    CONSTRAINT session_refresh_history_pkey PRIMARY KEY (refresh_hash),
    CONSTRAINT session_refresh_history_session_id_fkey FOREIGN KEY (session_id) REFERENCES session (id) ON DELETE CASCADE
);

CREATE INDEX session_refresh_history_session_id_idx ON session_refresh_history (session_id);

CREATE TABLE user_token (
    id             uuid        NOT NULL DEFAULT uuidv7(),
    user_id        uuid        NOT NULL,
    purpose        text        NOT NULL,
    token_hash     bytea       NOT NULL,
    expires_at     timestamptz NOT NULL,
    used_at        timestamptz,
    invalidated_at timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT user_token_pkey PRIMARY KEY (id),
    CONSTRAINT user_token_token_hash_key UNIQUE (token_hash),
    CONSTRAINT user_token_purpose_check CHECK (purpose IN ('email_verify', 'password_reset')),
    CONSTRAINT user_token_user_id_fkey FOREIGN KEY (user_id) REFERENCES app_user (id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX user_token_user_id_purpose_key ON user_token (user_id, purpose) WHERE used_at IS NULL AND invalidated_at IS NULL;
CREATE INDEX user_token_expires_at_idx ON user_token (expires_at);

-- +goose Down
DROP TABLE user_token;
DROP TABLE session_refresh_history;
DROP TABLE session;
DROP TABLE user_role;
DROP TABLE app_user;
