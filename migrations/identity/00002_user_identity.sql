-- +goose Up
CREATE TABLE user_identity (
    id         uuid        NOT NULL DEFAULT uuidv7(),
    user_id    uuid        NOT NULL,
    provider   text        NOT NULL,
    subject    text        NOT NULL,
    union_id   text,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT user_identity_pkey PRIMARY KEY (id),
    CONSTRAINT user_identity_provider_subject_key UNIQUE (provider, subject),
    CONSTRAINT user_identity_provider_check CHECK (provider IN ('wechat_miniapp', 'wechat_app', 'alipay')),
    CONSTRAINT user_identity_user_id_fkey FOREIGN KEY (user_id) REFERENCES app_user (id) ON DELETE CASCADE
);

CREATE INDEX user_identity_user_id_idx ON user_identity (user_id);
CREATE INDEX user_identity_union_id_idx ON user_identity (union_id);

-- +goose Down
DROP TABLE user_identity;
