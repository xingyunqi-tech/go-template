-- name: CreateUser :one
INSERT INTO app_user (id, email, phone, password_hash, display_name, email_verified_at)
VALUES (@id, sqlc.narg('email'), sqlc.narg('phone'), sqlc.narg('password_hash'), @display_name,
        CASE WHEN @email_verified::bool THEN now() END)
RETURNING *;

-- name: GetUser :one
SELECT *
FROM app_user
WHERE id = @id AND deleted_at IS NULL;

-- name: GetUserForUpdate :one
SELECT *
FROM app_user
WHERE id = @id AND deleted_at IS NULL
FOR UPDATE;

-- name: GetUserByEmail :one
SELECT *
FROM app_user
WHERE email = @email AND deleted_at IS NULL;

-- name: UpdateUser :one
UPDATE app_user
SET display_name = coalesce(sqlc.narg('display_name'), display_name),
    status = coalesce(sqlc.narg('status'), status),
    avatar_file_id = CASE WHEN @set_avatar::bool THEN sqlc.narg('avatar_file_id')::uuid ELSE avatar_file_id END,
    updated_at = now()
WHERE id = @id AND deleted_at IS NULL
RETURNING *;

-- name: SetUserPassword :execrows
UPDATE app_user
SET password_hash = @password_hash, updated_at = now()
WHERE id = @id AND deleted_at IS NULL;

-- name: MarkEmailVerified :execrows
UPDATE app_user
SET email_verified_at = coalesce(email_verified_at, now()), updated_at = now()
WHERE id = @id AND deleted_at IS NULL;

-- name: ListUsers :many
SELECT u.*
FROM app_user u
WHERE u.deleted_at IS NULL
  AND (sqlc.narg('keyword')::text IS NULL
       OR u.email ILIKE '%' || sqlc.narg('keyword')::text || '%' ESCAPE '\'
       OR u.phone LIKE '%' || sqlc.narg('keyword')::text || '%' ESCAPE '\'
       OR u.display_name ILIKE '%' || sqlc.narg('keyword')::text || '%' ESCAPE '\')
  AND (sqlc.narg('status')::text IS NULL OR u.status = sqlc.narg('status')::text)
  AND (sqlc.narg('role')::text IS NULL
       OR EXISTS (SELECT 1 FROM user_role r WHERE r.user_id = u.id AND r.role_code = sqlc.narg('role')::text))
ORDER BY
  CASE WHEN @sort1::text = 'email' THEN u.email END ASC,
  CASE WHEN @sort1::text = '-email' THEN u.email END DESC,
  CASE WHEN @sort1::text = 'createdAt' THEN u.created_at END ASC,
  CASE WHEN @sort1::text = '-createdAt' THEN u.created_at END DESC,
  CASE WHEN @sort2::text = 'email' THEN u.email END ASC,
  CASE WHEN @sort2::text = '-email' THEN u.email END DESC,
  CASE WHEN @sort2::text = 'createdAt' THEN u.created_at END ASC,
  CASE WHEN @sort2::text = '-createdAt' THEN u.created_at END DESC,
  u.id DESC
LIMIT @row_limit::bigint OFFSET @row_offset::bigint;

-- name: CountUsers :one
SELECT count(*)
FROM app_user u
WHERE u.deleted_at IS NULL
  AND (sqlc.narg('keyword')::text IS NULL
       OR u.email ILIKE '%' || sqlc.narg('keyword')::text || '%' ESCAPE '\'
       OR u.phone LIKE '%' || sqlc.narg('keyword')::text || '%' ESCAPE '\'
       OR u.display_name ILIKE '%' || sqlc.narg('keyword')::text || '%' ESCAPE '\')
  AND (sqlc.narg('status')::text IS NULL OR u.status = sqlc.narg('status')::text)
  AND (sqlc.narg('role')::text IS NULL
       OR EXISTS (SELECT 1 FROM user_role r WHERE r.user_id = u.id AND r.role_code = sqlc.narg('role')::text));

-- name: ListUserRoles :many
SELECT user_id, role_code
FROM user_role
WHERE user_id = ANY(@user_ids::uuid[])
ORDER BY user_id, role_code;

-- name: AddUserRole :exec
INSERT INTO user_role (user_id, role_code)
VALUES (@user_id, @role_code)
ON CONFLICT DO NOTHING;

-- name: TouchUser :exec
UPDATE app_user
SET updated_at = now()
WHERE id = @id;

-- name: DeleteUserRoles :exec
DELETE FROM user_role
WHERE user_id = @user_id;

-- name: LockAdmins :exec
SELECT pg_advisory_xact_lock(@lock_key::bigint);

-- name: CountOtherActiveAdmins :one
SELECT count(*)
FROM app_user u
JOIN user_role r ON r.user_id = u.id AND r.role_code = 'admin'
WHERE u.id <> @id AND u.status = 'active' AND u.deleted_at IS NULL;

-- name: GetUserByPhone :one
SELECT *
FROM app_user
WHERE phone = @phone AND deleted_at IS NULL;

-- name: SetUserPhone :one
UPDATE app_user
SET phone = @phone, updated_at = now()
WHERE id = @id AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteUser :execrows
UPDATE app_user
SET deleted_at = now(), updated_at = now()
WHERE id = @id AND deleted_at IS NULL;

-- name: PurgeUser :execrows
DELETE FROM app_user
WHERE id = @id AND deleted_at IS NOT NULL;

-- name: UserDeleted :one
SELECT (deleted_at IS NOT NULL)::bool AS deleted
FROM app_user
WHERE id = @id;
