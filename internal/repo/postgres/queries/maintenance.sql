-- name: DeleteExpiredSessions :execrows
DELETE FROM session
WHERE expires_at < @now;

-- name: DeleteSpentTokens :execrows
DELETE FROM user_token
WHERE expires_at < @now OR used_at IS NOT NULL OR invalidated_at IS NOT NULL;

-- name: ListUnpurgedUsers :many
SELECT id, deleted_at
FROM app_user
WHERE deleted_at < @before
  AND (sqlc.narg('after_deleted_at')::timestamptz IS NULL
       OR (deleted_at, id) > (sqlc.narg('after_deleted_at')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY deleted_at, id
LIMIT @row_limit;
