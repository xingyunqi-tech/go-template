-- name: InsertConsentIfAbsent :exec
INSERT INTO "user_consent" ("id", "userId", "type", "version", "ip")
VALUES (@id, @user_id, @type, @version, sqlc.narg('ip'))
ON CONFLICT ("userId", "type", "version") DO NOTHING;

-- name: ListLatestConsents :many
SELECT DISTINCT ON ("type") * FROM "user_consent"
WHERE "userId" = @user_id
ORDER BY "type", "agreedAt" DESC, "id" DESC;

-- name: GetConsent :one
SELECT * FROM "user_consent" WHERE "userId" = @user_id AND "type" = @type AND "version" = @version;

-- name: ConsentExists :one
SELECT EXISTS (
    SELECT 1 FROM "user_consent" WHERE "userId" = @user_id AND "type" = @type AND "version" = @version
);
