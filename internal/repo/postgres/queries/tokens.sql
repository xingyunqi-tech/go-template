-- name: InvalidateUserTokens :exec
UPDATE user_token
SET invalidated_at = now()
WHERE user_id = @user_id AND purpose = @purpose AND used_at IS NULL AND invalidated_at IS NULL;

-- name: CreateUserToken :exec
INSERT INTO user_token (id, user_id, purpose, token_hash, expires_at)
VALUES (@id, @user_id, @purpose, @token_hash, @expires_at);

-- name: ConsumeUserToken :one
UPDATE user_token
SET used_at = now()
WHERE token_hash = @token_hash
  AND purpose = @purpose
  AND used_at IS NULL
  AND invalidated_at IS NULL
  AND expires_at > now()
RETURNING user_id;
