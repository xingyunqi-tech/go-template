-- name: CreateSession :one
INSERT INTO session (id, user_id, refresh_hash, auth_time, expires_at, user_agent, ip, last_active_at)
VALUES (@id, @user_id, @refresh_hash, @auth_time, @expires_at, @user_agent, @ip, @auth_time)
RETURNING *;

-- name: GetSessionByRefreshForUpdate :one
SELECT *
FROM session
WHERE refresh_hash = @refresh_hash
FOR UPDATE;

-- name: GetSessionForUpdate :one
SELECT *
FROM session
WHERE id = @id
FOR UPDATE;

-- name: GetRefreshHistory :one
SELECT *
FROM session_refresh_history
WHERE refresh_hash = @refresh_hash;

-- name: InsertRefreshHistory :exec
INSERT INTO session_refresh_history (refresh_hash, session_id, rotated_at)
VALUES (@refresh_hash, @session_id, @rotated_at);

-- name: RotateSessionRefresh :exec
UPDATE session
SET refresh_hash = @refresh_hash, last_active_at = @last_active_at
WHERE id = @id;

-- name: RevokeSession :execrows
UPDATE session
SET revoked_at = now()
WHERE id = @id AND user_id = @user_id AND revoked_at IS NULL AND expires_at > now();

-- name: RevokeUserSessions :many
UPDATE session
SET revoked_at = now()
WHERE user_id = @user_id
  AND revoked_at IS NULL
  AND expires_at > now()
  AND (sqlc.narg('keep_id')::uuid IS NULL OR id <> sqlc.narg('keep_id')::uuid)
RETURNING id;

-- name: ListActiveSessions :many
SELECT *
FROM session
WHERE user_id = @user_id AND revoked_at IS NULL AND expires_at > now()
ORDER BY last_active_at DESC, id DESC;
