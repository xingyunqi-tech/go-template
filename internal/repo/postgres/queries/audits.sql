-- name: CreateAuditLog :exec
INSERT INTO audit_log (id, actor_sub, action, resource_type, resource_id, request_id, ip, data)
VALUES (@id, @actor_sub, @action, @resource_type, @resource_id, @request_id, @ip, @data);
