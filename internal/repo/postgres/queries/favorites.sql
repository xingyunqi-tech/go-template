-- name: CreateFavorite :one
INSERT INTO "favorite" ("userId", "targetType", "targetId", "title", "image", "extra", "subjectId")
VALUES (@user_id, @target_type, @target_id, sqlc.narg('title'), sqlc.narg('image'), sqlc.narg('extra'), sqlc.narg('subject_id'))
RETURNING *;

-- name: ListFavorites :many
SELECT * FROM "favorite"
WHERE "userId" = @user_id
  AND (sqlc.narg('target_type')::text IS NULL OR "targetType" = sqlc.narg('target_type'))
  AND (sqlc.narg('subject_id')::uuid IS NULL OR "subjectId" = sqlc.narg('subject_id'))
ORDER BY "createdAt" DESC
OFFSET @row_offset::bigint LIMIT @row_limit::bigint;

-- name: CountFavorites :one
SELECT count(*) FROM "favorite"
WHERE "userId" = @user_id
  AND (sqlc.narg('target_type')::text IS NULL OR "targetType" = sqlc.narg('target_type'))
  AND (sqlc.narg('subject_id')::uuid IS NULL OR "subjectId" = sqlc.narg('subject_id'));

-- name: GetFavorite :one
SELECT * FROM "favorite" WHERE "id" = @id;

-- name: GetFavoriteByTarget :one
SELECT * FROM "favorite"
WHERE "userId" = @user_id AND "targetType" = @target_type AND "targetId" = @target_id
LIMIT 1;

-- name: DeleteFavorite :exec
DELETE FROM "favorite" WHERE "id" = @id;

-- name: DeleteUserFavorites :exec
DELETE FROM "favorite" WHERE "userId" = @user_id;
