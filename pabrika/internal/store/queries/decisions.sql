-- Decision queries. Every name carries the Decision prefix. Rows are insert-only: there is no
-- update or delete query. Every reader joins tickets and hides soft-deleted ones.

-- name: DecisionInsert :exec
INSERT INTO ticket_decisions (id, ticket_id, project_id, gate, verdict, note, actor_user_id, actor_role, gate_label, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: DecisionGetView :one
SELECT d.id, d.ticket_id, d.gate, d.verdict, d.note, d.actor_user_id, d.actor_role, d.gate_label, d.created_at,
       u.display_name AS user_name, u.email AS user_email
FROM ticket_decisions d
JOIN tickets t ON t.id = d.ticket_id AND t.deleted_at IS NULL
LEFT JOIN users u ON u.id = d.actor_user_id
WHERE d.id = ?;

-- name: DecisionList :many
-- Newest first by (created_at, id); keyset in expanded OR form.
SELECT d.id, d.ticket_id, d.gate, d.verdict, d.note, d.actor_user_id, d.actor_role, d.gate_label, d.created_at,
       u.display_name AS user_name, u.email AS user_email
FROM ticket_decisions d
JOIN tickets t ON t.id = d.ticket_id AND t.deleted_at IS NULL
LEFT JOIN users u ON u.id = d.actor_user_id
WHERE d.ticket_id = sqlc.arg(ticket_id)
  AND (CAST(sqlc.arg(has_cursor) AS INTEGER) = 0
       OR d.created_at < CAST(sqlc.arg(cursor_created_at) AS TEXT)
       OR (d.created_at = CAST(sqlc.arg(cursor_created_at) AS TEXT) AND d.id < CAST(sqlc.arg(cursor_id) AS TEXT)))
ORDER BY d.created_at DESC, d.id DESC
LIMIT sqlc.arg(row_limit);

-- name: DecisionLatestPerGate :many
-- The newest decision of each gate of one ticket.
SELECT d.id, d.ticket_id, d.gate, d.verdict, d.note, d.actor_user_id, d.actor_role, d.gate_label, d.created_at,
       u.display_name AS user_name, u.email AS user_email
FROM ticket_decisions d
JOIN tickets t ON t.id = d.ticket_id AND t.deleted_at IS NULL
LEFT JOIN users u ON u.id = d.actor_user_id
WHERE d.ticket_id = sqlc.arg(ticket_id)
  AND d.id = (SELECT d2.id FROM ticket_decisions d2
              WHERE d2.ticket_id = d.ticket_id AND d2.gate = d.gate
              ORDER BY d2.created_at DESC, d2.id DESC LIMIT 1)
ORDER BY d.gate;
