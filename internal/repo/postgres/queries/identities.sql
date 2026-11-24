-- name: LockIdentityKey :exec
SELECT pg_advisory_xact_lock(hashtextextended(@lock_key::text, 0));

-- name: GetIdentity :one
SELECT *
FROM user_identity
WHERE provider = @provider AND subject = @subject;

-- name: GetIdentityByUnionID :one
SELECT *
FROM user_identity
WHERE union_id = @union_id AND provider = ANY(@providers::text[])
ORDER BY created_at
LIMIT 1;

-- name: CreateIdentity :exec
INSERT INTO user_identity (id, user_id, provider, subject, union_id)
VALUES (@id, @user_id, @provider, @subject, sqlc.narg('union_id'));

-- name: SetIdentityUnionID :exec
UPDATE user_identity
SET union_id = @union_id
WHERE provider = @provider AND subject = @subject AND union_id IS NULL;

-- name: DeleteUserIdentities :exec
DELETE FROM user_identity
WHERE user_id = @user_id;
