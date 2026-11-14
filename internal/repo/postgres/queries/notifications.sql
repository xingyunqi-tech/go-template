-- name: ListNotifications :many
SELECT * FROM "notification"
WHERE "userId" = @user_id AND "channels" @> '["IN_APP"]'::jsonb AND "deletedAt" IS NULL
  AND (sqlc.narg('category')::varchar IS NULL OR "category" = sqlc.narg('category')::varchar)
  AND (sqlc.narg('is_read')::boolean IS NULL OR "isRead" = sqlc.narg('is_read')::boolean)
ORDER BY "createdAt" DESC
OFFSET @row_offset LIMIT @row_limit;

-- name: CountNotifications :one
SELECT count(*) FROM "notification"
WHERE "userId" = @user_id AND "channels" @> '["IN_APP"]'::jsonb AND "deletedAt" IS NULL
  AND (sqlc.narg('category')::varchar IS NULL OR "category" = sqlc.narg('category')::varchar)
  AND (sqlc.narg('is_read')::boolean IS NULL OR "isRead" = sqlc.narg('is_read')::boolean);

-- name: GetNotification :one
SELECT * FROM "notification"
WHERE "id" = @id AND "userId" = @user_id AND "channels" @> '["IN_APP"]'::jsonb AND "deletedAt" IS NULL;

-- name: MarkNotificationRead :one
UPDATE "notification" SET "isRead" = true, "readAt" = @read_at, "updatedAt" = CURRENT_TIMESTAMP
WHERE "id" = @id AND "userId" = @user_id AND "channels" @> '["IN_APP"]'::jsonb AND "deletedAt" IS NULL
RETURNING *;

-- name: MarkAllNotificationsRead :exec
UPDATE "notification" SET "isRead" = true, "readAt" = @read_at, "updatedAt" = CURRENT_TIMESTAMP
WHERE "userId" = @user_id AND "isRead" = false AND "channels" @> '["IN_APP"]'::jsonb;

-- name: SoftDeleteNotification :execrows
UPDATE "notification" SET "deletedAt" = CURRENT_TIMESTAMP, "updatedAt" = CURRENT_TIMESTAMP
WHERE "id" = @id AND "userId" = @user_id AND "channels" @> '["IN_APP"]'::jsonb AND "deletedAt" IS NULL;

-- name: CreateNotification :one
INSERT INTO "notification" ("userId", "type", "category", "title", "content", "metadata", "channels", "sentChannels", "isRead")
VALUES (@user_id, @type, @category, @title, @content, sqlc.narg('metadata')::jsonb, @channels::jsonb, '{}'::jsonb, false)
RETURNING *;

-- name: InsertInAppNotifications :execrows
INSERT INTO "notification" ("userId", "type", "category", "title", "content", "metadata", "channels")
SELECT target."userId", @type::varchar, @category::varchar, @title::varchar, @content::text,
  sqlc.narg('metadata')::jsonb, '["IN_APP"]'::jsonb
FROM unnest(@user_ids::int[]) AS target("userId")
WHERE NOT EXISTS (
  SELECT 1 FROM "notification_setting" AS setting
  WHERE setting."userId" = target."userId" AND setting."category" = @category::varchar AND NOT setting."enableInApp"
);

-- name: RecordNotificationChannel :exec
UPDATE "notification" SET "sentChannels" = "sentChannels" || @patch::jsonb, "updatedAt" = CURRENT_TIMESTAMP
WHERE "id" = @id AND "deletedAt" IS NULL;

-- name: DeleteUserNotifications :exec
DELETE FROM "notification" WHERE "userId" = @user_id;

-- name: GetNotificationSetting :one
SELECT * FROM "notification_setting" WHERE "userId" = @user_id AND "category" = @category;

-- name: ListNotificationSettings :many
SELECT * FROM "notification_setting" WHERE "userId" = @user_id;

-- name: CreateNotificationSetting :one
INSERT INTO "notification_setting" ("userId", "category", "enableInApp", "enableEmail", "enableSms", "enablePush")
VALUES (@user_id, @category, @enable_in_app, @enable_email, @enable_sms, @enable_push)
RETURNING *;

-- name: UpdateNotificationSetting :one
UPDATE "notification_setting"
SET "enableInApp" = @enable_in_app, "enableEmail" = @enable_email, "enableSms" = @enable_sms,
  "enablePush" = @enable_push, "updatedAt" = CURRENT_TIMESTAMP
WHERE "id" = @id
RETURNING *;

-- name: InitNotificationSettings :exec
INSERT INTO "notification_setting" ("userId", "category")
SELECT @user_id::int, category FROM unnest(@categories::varchar[]) WITH ORDINALITY AS c(category, position)
ORDER BY position
ON CONFLICT ON CONSTRAINT "UQ_notification_setting_userId_category" DO NOTHING;

-- name: DeleteUserNotificationSettings :exec
DELETE FROM "notification_setting" WHERE "userId" = @user_id;
