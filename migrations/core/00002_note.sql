-- +goose Up
CREATE TABLE note (
    id uuid NOT NULL DEFAULT uuidv7(),
    owner_sub uuid NOT NULL,
    title text NOT NULL,
    content text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CONSTRAINT note_pkey PRIMARY KEY (id)
);

CREATE INDEX note_owner_sub_created_at_idx ON note (owner_sub, created_at DESC, id DESC) WHERE deleted_at IS NULL;

-- +goose Down
DROP TABLE note;
