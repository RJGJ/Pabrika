-- name: ProjectInsert :one
INSERT INTO projects (id, key, name, description, next_ticket_number, created_by, created_at, updated_at)
VALUES (?, ?, ?, ?, 1, ?, ?, ?)
RETURNING *;

-- name: ProjectInsertOwner :exec
INSERT INTO project_members (project_id, user_id, role, created_at)
VALUES (?, ?, 'owner', ?);

-- name: ProjectKeyExists :one
SELECT EXISTS (SELECT 1 FROM projects WHERE key = ?);

-- name: ProjectListForUser :many
-- Member projects ordered by lower(name), then key. only_project ('' = no limit) serves
-- project-limited tokens; include_archived 0 hides archived projects.
SELECT p.id, p.key, p.name, p.description, p.next_ticket_number, p.created_by,
       p.archived_at, p.created_at, p.updated_at, m.role AS member_role
FROM projects p
JOIN project_members m ON m.project_id = p.id AND m.user_id = sqlc.arg(user_id)
WHERE (CAST(sqlc.arg(include_archived) AS INTEGER) = 1 OR p.archived_at IS NULL)
  AND (CAST(sqlc.arg(only_project) AS TEXT) = '' OR p.id = CAST(sqlc.arg(only_project) AS TEXT))
ORDER BY lower(p.name), p.key;

-- name: ProjectStatusCounts :many
-- Live (not soft-deleted) ticket counts per project and status.
SELECT project_id, status, COUNT(*) AS n
FROM tickets
WHERE deleted_at IS NULL AND project_id IN (sqlc.slice('project_ids'))
GROUP BY project_id, status;

-- name: ProjectUpdate :one
UPDATE projects SET name = ?, description = ?, archived_at = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: ProjectDelete :exec
DELETE FROM projects WHERE id = ?;
