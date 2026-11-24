-- name: CreateAuditLog :exec
INSERT INTO audit_log (id, actor_sub, action, resource_type, resource_id, request_id, ip, data)
VALUES (@id, @actor_sub, @action, @resource_type, @resource_id, @request_id, @ip, @data);

-- name: AnonymizeActor :exec
UPDATE audit_log
SET actor_sub = NULL, ip = ''
WHERE actor_sub = @actor_sub;

-- name: ListAuditLogs :many
SELECT *
FROM audit_log
WHERE (sqlc.narg('actor_sub')::uuid IS NULL OR actor_sub = sqlc.narg('actor_sub')::uuid)
  AND (sqlc.narg('action')::text IS NULL OR action = sqlc.narg('action')::text)
  AND (sqlc.narg('after_created_at')::timestamptz IS NULL
       OR (created_at, id) < (sqlc.narg('after_created_at')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY created_at DESC, id DESC
LIMIT @row_limit;

-- name: DeleteAuditLogsBefore :execrows
DELETE FROM audit_log
WHERE id IN (SELECT old.id FROM audit_log old WHERE old.created_at < @before LIMIT @row_limit);
