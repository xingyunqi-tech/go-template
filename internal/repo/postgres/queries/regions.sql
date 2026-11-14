-- name: GetRegionByCode :one
SELECT * FROM "region" WHERE "code" = @code;

-- name: ListRegionsByLevel :many
SELECT * FROM "region" WHERE "level" = @level ORDER BY "sort", "code";

-- name: ListRegionsByParent :many
SELECT * FROM "region" WHERE "parentCode" = @parent_code ORDER BY "sort", "code";

-- name: ListRegions :many
SELECT * FROM "region" ORDER BY "level", "sort", "code";

-- name: SearchRegions :many
SELECT * FROM "region" WHERE "name" ILIKE @pattern ORDER BY "level", "sort" LIMIT @row_limit;
