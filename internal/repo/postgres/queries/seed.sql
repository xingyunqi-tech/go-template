-- name: SeedRole :exec
INSERT INTO "role" ("id", "name") VALUES (@id, @name) ON CONFLICT DO NOTHING;

-- name: SeedStatus :exec
INSERT INTO "status" ("id", "name") VALUES (@id, @name) ON CONFLICT DO NOTHING;

-- name: SeedPermission :exec
INSERT INTO "permission" ("id", "name", "description", "resource", "action")
VALUES (@id, @name, sqlc.narg('description'), @resource, @action) ON CONFLICT DO NOTHING;

-- name: SeedRolePermission :exec
INSERT INTO "role_permission" ("role_id", "permission_id") VALUES (@role_id, @permission_id) ON CONFLICT DO NOTHING;

-- name: CountRegions :one
SELECT count(*) FROM "region";

-- name: SeedRegions :copyfrom
INSERT INTO "region" ("code", "name", "level", "parentCode", "sort") VALUES (@code, @name, @level, @parent_code, @sort);

-- name: SeedConfig :exec
INSERT INTO "config" ("key", "value", "isPublic") VALUES (@key, @value, @is_public) ON CONFLICT DO NOTHING;
