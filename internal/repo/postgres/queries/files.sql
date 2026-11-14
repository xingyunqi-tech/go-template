-- name: GetFiles :many
SELECT * FROM "file" WHERE "id" = ANY(@ids::uuid[]);
