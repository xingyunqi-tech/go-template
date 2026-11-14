-- name: GetConfig :one
SELECT * FROM "config" WHERE "key" = @key;

-- name: UpsertConfig :one
INSERT INTO "config" ("key", "value", "isPublic")
VALUES (@key, @value, COALESCE(sqlc.narg('is_public')::boolean, false))
ON CONFLICT ("key") DO UPDATE
SET "value" = EXCLUDED."value",
    "isPublic" = COALESCE(sqlc.narg('is_public')::boolean, "config"."isPublic"),
    "updatedAt" = now()
WHERE "config"."value" IS DISTINCT FROM EXCLUDED."value"
   OR "config"."isPublic" IS DISTINCT FROM COALESCE(sqlc.narg('is_public')::boolean, "config"."isPublic")
RETURNING *;
