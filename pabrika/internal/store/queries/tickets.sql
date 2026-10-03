-- Ticket queries. Every name carries the Ticket prefix (names are global across all .sql files).

-- name: TicketAllocateNumber :one
-- Atomic per-project counter; runs inside the create transaction.
UPDATE projects SET next_ticket_number = next_ticket_number + 1
WHERE id = ?
RETURNING next_ticket_number - 1 AS number;

-- name: TicketInsert :exec
INSERT INTO tickets (id, project_id, number, title, description, status, priority, assignee_id, position, due_date, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: TicketGet :one
-- A live (not soft-deleted) ticket by id.
SELECT * FROM tickets WHERE id = ? AND deleted_at IS NULL;

-- name: TicketUpdateFields :exec
UPDATE tickets
SET title = sqlc.arg(title), description = sqlc.arg(description), priority = sqlc.arg(priority),
    due_date = sqlc.arg(due_date), assignee_id = sqlc.arg(assignee_id), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: TicketUpdateMove :exec
UPDATE tickets SET status = ?, position = ?, updated_at = ? WHERE id = ?;

-- name: TicketSetPosition :exec
-- Renumbering: position only, no updated_at bump.
UPDATE tickets SET position = ? WHERE id = ?;

-- name: TicketSoftDelete :execrows
UPDATE tickets SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL;

-- name: TicketListColumn :many
-- Live tickets of one column in (position, id) order, excluding one ticket id ('' excludes none).
SELECT id, position FROM tickets
WHERE project_id = sqlc.arg(project_id) AND status = sqlc.arg(status) AND deleted_at IS NULL
  AND id <> sqlc.arg(exclude_id)
ORDER BY position, id;

-- name: TicketListPage :many
-- Board order: status rank, position, id. Keyset predicate in expanded OR form (no row values).
-- description is intentionally not selected. query_text is a literal substring (instr, so no
-- LIKE wildcards to escape; lower() is ASCII-only, as the spec accepts).
SELECT t.id, t.project_id, t.number, t.title, t.status, t.priority, t.assignee_id, t.position,
       t.due_date, t.created_at, t.updated_at,
       CAST(CASE t.status WHEN 'backlog' THEN 0 WHEN 'todo' THEN 1 WHEN 'in_progress' THEN 2 ELSE 3 END AS INTEGER) AS status_rank
FROM tickets t
WHERE t.project_id = sqlc.arg(project_id) AND t.deleted_at IS NULL
  AND (CAST(sqlc.narg(status) AS TEXT) IS NULL OR t.status = CAST(sqlc.narg(status) AS TEXT))
  AND (CAST(sqlc.narg(priority) AS TEXT) IS NULL OR t.priority = CAST(sqlc.narg(priority) AS TEXT))
  AND (CAST(sqlc.arg(unassigned) AS INTEGER) = 0 OR t.assignee_id IS NULL)
  AND (CAST(sqlc.narg(assignee_id) AS TEXT) IS NULL OR t.assignee_id = CAST(sqlc.narg(assignee_id) AS TEXT))
  AND (CAST(sqlc.narg(label_id) AS TEXT) IS NULL OR EXISTS (
        SELECT 1 FROM ticket_labels tl WHERE tl.ticket_id = t.id AND tl.label_id = CAST(sqlc.narg(label_id) AS TEXT)))
  AND (CAST(sqlc.narg(query_text) AS TEXT) IS NULL
       OR instr(lower(t.title), lower(CAST(sqlc.narg(query_text) AS TEXT))) > 0
       OR instr(lower(t.description), lower(CAST(sqlc.narg(query_text) AS TEXT))) > 0)
  AND (CAST(sqlc.arg(has_cursor) AS INTEGER) = 0
       OR CASE t.status WHEN 'backlog' THEN 0 WHEN 'todo' THEN 1 WHEN 'in_progress' THEN 2 ELSE 3 END > CAST(sqlc.arg(cursor_rank) AS INTEGER)
       OR (CASE t.status WHEN 'backlog' THEN 0 WHEN 'todo' THEN 1 WHEN 'in_progress' THEN 2 ELSE 3 END = CAST(sqlc.arg(cursor_rank) AS INTEGER)
           AND t.position > CAST(sqlc.arg(cursor_position) AS REAL))
       OR (CASE t.status WHEN 'backlog' THEN 0 WHEN 'todo' THEN 1 WHEN 'in_progress' THEN 2 ELSE 3 END = CAST(sqlc.arg(cursor_rank) AS INTEGER)
           AND t.position = CAST(sqlc.arg(cursor_position) AS REAL)
           AND t.id > CAST(sqlc.arg(cursor_id) AS TEXT)))
ORDER BY status_rank, t.position, t.id
LIMIT sqlc.arg(row_limit);

-- name: TicketLabelsForTickets :many
-- Batched label hydration (name order via the NOCASE column collation, then id).
SELECT tl.ticket_id, l.id, l.project_id, l.name, l.color
FROM ticket_labels tl JOIN labels l ON l.id = tl.label_id
WHERE tl.ticket_id IN (sqlc.slice('ticket_ids'))
ORDER BY tl.ticket_id, l.name, l.id;

-- name: TicketUsersByIDs :many
-- Batched assignee hydration.
SELECT id, email, display_name FROM users WHERE id IN (sqlc.slice('user_ids'));

-- name: TicketCommentCounts :many
-- Batched comment counts (non-deleted comments only).
SELECT ticket_id, COUNT(*) AS comment_count FROM comments
WHERE ticket_id IN (sqlc.slice('ticket_ids')) AND deleted_at IS NULL
GROUP BY ticket_id;

-- name: TicketLabelsInProject :many
-- Labels of one project among the given ids, in name order (validates ownership and gives names).
SELECT id, project_id, name, color FROM labels
WHERE project_id = sqlc.arg(project_id) AND id IN (sqlc.slice('label_ids'))
ORDER BY name, id;

-- name: TicketClearLabels :exec
DELETE FROM ticket_labels WHERE ticket_id = ?;

-- name: TicketAddLabel :exec
INSERT INTO ticket_labels (ticket_id, label_id) VALUES (?, ?);

-- name: TicketIsMember :one
SELECT COUNT(*) FROM project_members WHERE project_id = ? AND user_id = ?;

-- name: TicketSeedLabel :one
-- Test seeding only: a label without going through the Labels service.
INSERT INTO labels (id, project_id, name, color) VALUES (?, ?, ?, ?)
RETURNING *;

-- name: TicketSeedGetRaw :one
-- Test only: a ticket row including soft-deleted ones.
SELECT * FROM tickets WHERE id = ?;
