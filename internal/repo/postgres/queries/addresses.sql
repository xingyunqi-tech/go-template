-- name: LockUserAddresses :exec
SELECT pg_advisory_xact_lock(hashtext('address'), @user_id::int);

-- name: CountUserAddresses :one
SELECT count(*) FROM "address"
WHERE "userId" = @user_id AND "deletedAt" IS NULL
  AND (sqlc.narg('province')::text IS NULL OR "province" = sqlc.narg('province'))
  AND (sqlc.narg('city')::text IS NULL OR "city" = sqlc.narg('city'))
  AND (sqlc.narg('province_code')::text IS NULL OR "provinceCode" = sqlc.narg('province_code'))
  AND (sqlc.narg('city_code')::text IS NULL OR "cityCode" = sqlc.narg('city_code'))
  AND (sqlc.narg('district_code')::text IS NULL OR "districtCode" = sqlc.narg('district_code'));

-- name: CreateAddress :one
INSERT INTO "address" (
    "userId", "name", "phone", "provinceCode", "cityCode", "districtCode", "province", "city",
    "district", "address", "isDefault"
) VALUES (
    @user_id, @name, @phone, sqlc.narg('province_code'), sqlc.narg('city_code'), sqlc.narg('district_code'),
    @province, @city, @district, @address, false
)
RETURNING *;

-- name: GetUserAddress :one
SELECT * FROM "address" WHERE "id" = @id AND "userId" = @user_id AND "deletedAt" IS NULL;

-- name: GetActiveAddress :one
SELECT * FROM "address" WHERE "id" = @id AND "deletedAt" IS NULL;

-- name: GetDefaultAddress :one
SELECT * FROM "address" WHERE "userId" = @user_id AND "isDefault" AND "deletedAt" IS NULL LIMIT 1;

-- name: UpdateAddress :execrows
UPDATE "address" SET
    "name" = CASE WHEN @set_name::bool THEN sqlc.narg('name') ELSE "name" END,
    "phone" = CASE WHEN @set_phone::bool THEN sqlc.narg('phone') ELSE "phone" END,
    "provinceCode" = CASE WHEN @set_province_code::bool THEN sqlc.narg('province_code') ELSE "provinceCode" END,
    "cityCode" = CASE WHEN @set_city_code::bool THEN sqlc.narg('city_code') ELSE "cityCode" END,
    "districtCode" = CASE WHEN @set_district_code::bool THEN sqlc.narg('district_code') ELSE "districtCode" END,
    "province" = CASE WHEN @set_province::bool THEN sqlc.narg('province') ELSE "province" END,
    "city" = CASE WHEN @set_city::bool THEN sqlc.narg('city') ELSE "city" END,
    "district" = CASE WHEN @set_district::bool THEN sqlc.narg('district') ELSE "district" END,
    "address" = CASE WHEN @set_address::bool THEN sqlc.narg('address') ELSE "address" END,
    "isDefault" = CASE WHEN @set_is_default::bool THEN sqlc.narg('is_default') ELSE "isDefault" END,
    "updatedAt" = now()
WHERE "id" = @id AND "deletedAt" IS NULL;

-- name: ClearOtherDefaultAddresses :exec
UPDATE "address" SET "isDefault" = false
WHERE "userId" = @user_id AND "isDefault" AND "id" <> @id AND "deletedAt" IS NULL;

-- name: SetDefaultAddress :exec
UPDATE "address" SET "isDefault" = true WHERE "id" = @id AND "userId" = @user_id AND "deletedAt" IS NULL;

-- name: SoftDeleteAddress :exec
UPDATE "address" SET "deletedAt" = now(), "updatedAt" = now() WHERE "id" = @id AND "deletedAt" IS NULL;

-- name: DeleteUserAddresses :exec
DELETE FROM "address" WHERE "userId" = @user_id;

-- name: GetAddressRegionsByCode :many
SELECT "code", "name", "level", "parentCode" FROM "region" WHERE "code" = ANY(@codes::text[]);
