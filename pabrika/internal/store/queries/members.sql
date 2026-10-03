-- name: MemberList :many
-- Owners first, then display name (case-insensitive), then user id.
SELECT m.user_id, u.email, u.display_name, m.role, m.created_at
FROM project_members m
JOIN users u ON u.id = m.user_id
WHERE m.project_id = ?
ORDER BY CASE m.role WHEN 'owner' THEN 0 ELSE 1 END, lower(u.display_name), m.user_id;

-- name: MemberGet :one
SELECT m.user_id, u.email, u.display_name, m.role, m.created_at
FROM project_members m
JOIN users u ON u.id = m.user_id
WHERE m.project_id = ? AND m.user_id = ?;

-- name: MemberInsert :exec
INSERT INTO project_members (project_id, user_id, role, created_at)
VALUES (?, ?, ?, ?);

-- name: MemberUpdateRole :execrows
UPDATE project_members SET role = ? WHERE project_id = ? AND user_id = ?;

-- name: MemberDelete :execrows
DELETE FROM project_members WHERE project_id = ? AND user_id = ?;

-- name: MemberCountOwners :one
SELECT COUNT(*) FROM project_members WHERE project_id = ? AND role = 'owner';

-- name: MemberListLiveAssignedTickets :many
-- Live tickets of the project currently assigned to the user (for the activity rows).
SELECT id FROM tickets
WHERE project_id = ? AND assignee_id = ? AND deleted_at IS NULL
ORDER BY id;

-- name: MemberClearAssignee :execrows
-- Unassigns the user from every ticket of the project, soft-deleted ones included.
UPDATE tickets SET assignee_id = NULL WHERE project_id = ? AND assignee_id = ?;

-- name: MemberInsertActivity :exec
INSERT INTO ticket_activity (id, ticket_id, actor_type, actor_id, action, changes, created_at)
VALUES (?, ?, ?, ?, 'assigned', ?, ?);
