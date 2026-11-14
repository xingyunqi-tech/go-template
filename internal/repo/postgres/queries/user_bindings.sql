-- name: BindPhoneIfEmpty :execrows
UPDATE "user" SET "phone" = @phone, "updatedAt" = now()
WHERE "id" = @id AND "phone" IS NULL AND "deletedAt" IS NULL;

-- name: BindWechatIfEmpty :execrows
UPDATE "user" SET
    "wechatOpenId" = @open_id,
    "wechatUnionId" = COALESCE(sqlc.narg('union_id'), "wechatUnionId"),
    "updatedAt" = now()
WHERE "id" = @id AND "wechatOpenId" IS NULL AND "deletedAt" IS NULL;
