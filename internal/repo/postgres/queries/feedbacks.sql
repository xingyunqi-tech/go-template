-- name: CreateFeedback :one
INSERT INTO "feedback" ("userId", "type", "content", "images", "contact", "status")
VALUES (@user_id, @type, @content, sqlc.narg('images'), sqlc.narg('contact'), 'pending')
RETURNING *;

-- name: GetFeedback :one
SELECT * FROM "feedback" WHERE "id" = @id;

-- name: ListUserFeedbacks :many
SELECT * FROM "feedback"
WHERE "userId" = @user_id AND "type" = ANY(@types::text[])
  AND (sqlc.narg('status')::text IS NULL OR "status" = sqlc.narg('status'))
ORDER BY "createdAt" DESC, "id" DESC
OFFSET @row_offset::bigint LIMIT @row_limit::bigint;

-- name: CountUserFeedbacks :one
SELECT count(*) FROM "feedback"
WHERE "userId" = @user_id AND "type" = ANY(@types::text[])
  AND (sqlc.narg('status')::text IS NULL OR "status" = sqlc.narg('status'));

-- name: AnonymizeUserFeedbacks :exec
UPDATE "feedback" SET "userId" = NULL, "contact" = NULL, "images" = NULL, "updatedAt" = now()
WHERE "userId" = @user_id;
