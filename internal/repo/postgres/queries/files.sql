-- name: GetFiles :many
SELECT * FROM "file" WHERE "id" = ANY(@ids::uuid[]);

-- name: CreateFile :one
INSERT INTO "file" ("path", "ownerUserId", "visibility", "mimeType", "size")
VALUES (@path, sqlc.narg(owner_user_id), @visibility, sqlc.narg(mime_type), sqlc.narg(size))
RETURNING *;

-- name: GetFile :one
SELECT * FROM "file" WHERE "id" = @id;

-- name: GetFileByPath :one
SELECT * FROM "file" WHERE "path" = @path LIMIT 1;

-- name: ListFilesByOwner :many
SELECT * FROM "file"
WHERE "ownerUserId" = @owner_user_id AND (sqlc.narg(after_id)::uuid IS NULL OR "id" > sqlc.narg(after_id)::uuid)
ORDER BY "id"
LIMIT @lim;

-- name: DeleteFile :exec
DELETE FROM "file" WHERE "id" = @id;
