-- name: ListRolePermissions :many
SELECT "role_id", "permission_id" FROM "role_permission" ORDER BY "role_id", "createdAt", "permission_id";
