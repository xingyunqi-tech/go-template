-- +goose Up
CREATE TABLE file (
    id           uuid        NOT NULL DEFAULT uuidv7(),
    owner_sub    uuid        NOT NULL,
    purpose      text        NOT NULL,
    name         text        NOT NULL,
    content_type text        NOT NULL,
    size         bigint      NOT NULL,
    sha256       bytea       NOT NULL,
    storage      text        NOT NULL,
    object_key   text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz,
    CONSTRAINT file_pkey PRIMARY KEY (id),
    CONSTRAINT file_object_key_key UNIQUE (object_key),
    CONSTRAINT file_storage_check CHECK (storage IN ('local', 's3')),
    CONSTRAINT file_size_check CHECK (size >= 0)
);

CREATE INDEX file_owner_sub_idx ON file (owner_sub);

-- +goose Down
DROP TABLE file;
