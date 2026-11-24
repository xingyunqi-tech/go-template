-- name: CreateNote :one
INSERT INTO note (id, owner_sub, title, content)
VALUES (@id, @owner_sub, @title, @content)
RETURNING *;

-- name: GetNote :one
SELECT *
FROM note
WHERE id = @id AND owner_sub = @owner_sub AND deleted_at IS NULL;

-- name: ListNotes :many
SELECT *
FROM note
WHERE owner_sub = @owner_sub
  AND deleted_at IS NULL
  AND (sqlc.narg('after_created_at')::timestamptz IS NULL
       OR (created_at, id) < (sqlc.narg('after_created_at')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY created_at DESC, id DESC
LIMIT @row_limit;

-- name: UpdateNote :one
UPDATE note
SET title = coalesce(sqlc.narg('title'), title),
    content = coalesce(sqlc.narg('content'), content),
    updated_at = now()
WHERE id = @id AND owner_sub = @owner_sub AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteNote :execrows
UPDATE note
SET deleted_at = now()
WHERE id = @id AND owner_sub = @owner_sub AND deleted_at IS NULL;

-- name: PurgeOwnerNotes :exec
DELETE FROM note
WHERE owner_sub = @owner_sub;
