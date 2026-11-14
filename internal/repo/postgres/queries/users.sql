-- name: CreateUser :one
INSERT INTO "user" (
    "firstName", "lastName", "email", "phone", "nickname", "gender", "birthday", "password",
    "provider", "wechatOpenId", "wechatUnionId", "alipayOpenId", "photoId", "roleId", "statusId"
) VALUES (
    sqlc.narg('first_name'), sqlc.narg('last_name'), sqlc.narg('email'), sqlc.narg('phone'),
    sqlc.narg('nickname'), sqlc.narg('gender'), sqlc.narg('birthday'), sqlc.narg('password'),
    @provider, sqlc.narg('wechat_open_id'), sqlc.narg('wechat_union_id'), sqlc.narg('alipay_open_id'),
    sqlc.narg('photo_id'), sqlc.narg('role_id'), sqlc.narg('status_id')
)
RETURNING "id";

-- name: UpdateUser :execrows
UPDATE "user" SET
    "firstName" = CASE WHEN @set_first_name::bool THEN sqlc.narg('first_name') ELSE "firstName" END,
    "lastName" = CASE WHEN @set_last_name::bool THEN sqlc.narg('last_name') ELSE "lastName" END,
    "email" = CASE WHEN @set_email::bool THEN sqlc.narg('email') ELSE "email" END,
    "phone" = CASE WHEN @set_phone::bool THEN sqlc.narg('phone') ELSE "phone" END,
    "nickname" = CASE WHEN @set_nickname::bool THEN sqlc.narg('nickname') ELSE "nickname" END,
    "gender" = CASE WHEN @set_gender::bool THEN sqlc.narg('gender') ELSE "gender" END,
    "birthday" = CASE WHEN @set_birthday::bool THEN sqlc.narg('birthday') ELSE "birthday" END,
    "password" = CASE WHEN @set_password::bool THEN sqlc.narg('password') ELSE "password" END,
    "provider" = CASE WHEN @set_provider::bool THEN sqlc.narg('provider') ELSE "provider" END,
    "photoId" = CASE WHEN @set_photo::bool THEN sqlc.narg('photo_id') ELSE "photoId" END,
    "roleId" = COALESCE(sqlc.narg('role_id'), "roleId"),
    "statusId" = COALESCE(sqlc.narg('status_id'), "statusId"),
    "updatedAt" = now()
WHERE "id" = @id AND "deletedAt" IS NULL;

-- name: ReplacePassword :execrows
UPDATE "user" SET "password" = @new_password, "updatedAt" = now()
WHERE "id" = @id AND "deletedAt" IS NULL AND "password" IS NOT DISTINCT FROM sqlc.narg('old_password');

-- name: RehashPassword :exec
UPDATE "user" SET "password" = @new_password
WHERE "id" = @id AND "password" = @old_password;

-- name: RemoveUser :exec
UPDATE "user" SET
    "email" = NULL, "phone" = NULL, "wechatOpenId" = NULL, "wechatUnionId" = NULL, "alipayOpenId" = NULL,
    "deletedAt" = COALESCE("deletedAt", now()), "updatedAt" = now()
WHERE "id" = @id;

-- name: CountActiveUsersByRole :one
SELECT count(*) FROM "user" WHERE "roleId" = @role_id AND "deletedAt" IS NULL;
