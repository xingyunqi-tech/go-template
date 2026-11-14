-- name: GetSession :one
SELECT * FROM "session" WHERE "id" = @id AND "deletedAt" IS NULL;

-- name: CreateSession :one
INSERT INTO "session" ("userId", "hash", "userAgent", "ip", "lastActiveAt")
VALUES (@user_id, @hash, sqlc.narg('user_agent'), sqlc.narg('ip'), now())
RETURNING *;

-- name: RotateSessionHash :execrows
UPDATE "session" SET "hash" = @next, "lastActiveAt" = now(), "updatedAt" = now()
WHERE "id" = @id AND "hash" = @current AND "deletedAt" IS NULL;

-- name: ListActiveSessions :many
SELECT * FROM "session"
WHERE "userId" = @user_id AND "deletedAt" IS NULL
ORDER BY "lastActiveAt" DESC NULLS LAST, "id" DESC;

-- name: AuthenticateSession :one
SELECT s."id" AS session_id, u."id" AS user_id, u."statusId" AS status_id, u."roleId" AS role_id
FROM "session" s
JOIN "user" u ON u."id" = s."userId" AND u."deletedAt" IS NULL
WHERE s."id" = @id AND s."deletedAt" IS NULL;

-- name: TouchSession :exec
UPDATE "session" SET "lastActiveAt" = now(), "updatedAt" = now()
WHERE "id" = @id AND ("lastActiveAt" IS NULL OR "lastActiveAt" < now() - interval '5 minutes');

-- name: SoftDeleteUserSession :execrows
UPDATE "session" SET "deletedAt" = now(), "updatedAt" = now()
WHERE "id" = @id AND "userId" = @user_id AND "deletedAt" IS NULL;

-- name: SoftDeleteSession :exec
UPDATE "session" SET "deletedAt" = now(), "updatedAt" = now()
WHERE "id" = @id AND "deletedAt" IS NULL;

-- name: SoftDeleteUserSessions :many
UPDATE "session" SET "deletedAt" = now(), "updatedAt" = now()
WHERE "userId" = @user_id AND "deletedAt" IS NULL AND "id" <> COALESCE(sqlc.narg('except_id')::int, -1)
RETURNING "id";
