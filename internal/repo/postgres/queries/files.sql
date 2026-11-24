-- name: CreateFile :one
INSERT INTO file (id, owner_sub, purpose, name, content_type, size, sha256, storage, object_key)
VALUES (@id, @owner_sub, @purpose, @name, @content_type, @size, @sha256, @storage, @object_key)
RETURNING *;

-- name: GetFile :one
SELECT *
FROM file
WHERE id = @id AND deleted_at IS NULL;

-- name: ListFilesByID :many
SELECT *
FROM file
WHERE id = ANY(@ids::uuid[]) AND deleted_at IS NULL;

-- name: ListOwnerFiles :many
SELECT *
FROM file
WHERE owner_sub = @owner_sub;

-- name: DeleteOwnerFiles :exec
DELETE FROM file
WHERE owner_sub = @owner_sub;
