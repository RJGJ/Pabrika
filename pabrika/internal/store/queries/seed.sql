-- Direct inserts used by internal/testutil to seed data without going through services.

-- name: SeedProject :one
INSERT INTO projects (id, key, name, description, next_ticket_number, created_by, created_at, updated_at)
VALUES (?, ?, ?, '', 1, ?, ?, ?)
RETURNING *;

-- name: SeedMember :exec
INSERT INTO project_members (project_id, user_id, role, created_at)
VALUES (?, ?, ?, ?);

-- name: SeedArchiveProject :exec
UPDATE projects SET archived_at = ? WHERE id = ?;
