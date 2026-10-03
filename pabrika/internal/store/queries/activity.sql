-- Activity queries. Every name carries the Activity prefix.

-- name: ActivityInsert :exec
INSERT INTO ticket_activity (id, ticket_id, actor_type, actor_id, action, changes, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ActivityList :many
-- Newest first by (created_at, id); keyset in expanded OR form. Names via LEFT JOINs.
SELECT a.id, a.ticket_id, a.actor_type, a.actor_id, a.action, a.changes, a.created_at,
       u.display_name AS user_name, tk.name AS token_name, ou.display_name AS owner_name
FROM ticket_activity a
LEFT JOIN users u ON a.actor_type = 'user' AND u.id = a.actor_id
LEFT JOIN api_tokens tk ON a.actor_type = 'api_token' AND tk.id = a.actor_id
LEFT JOIN users ou ON ou.id = tk.user_id
WHERE a.ticket_id = sqlc.arg(ticket_id)
  AND (CAST(sqlc.arg(has_cursor) AS INTEGER) = 0
       OR a.created_at < CAST(sqlc.arg(cursor_created_at) AS TEXT)
       OR (a.created_at = CAST(sqlc.arg(cursor_created_at) AS TEXT) AND a.id < CAST(sqlc.arg(cursor_id) AS TEXT)))
ORDER BY a.created_at DESC, a.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ActivitySeedListRaw :many
-- Test only: raw activity rows of a ticket (including soft-deleted tickets), oldest first.
SELECT * FROM ticket_activity WHERE ticket_id = ? ORDER BY created_at, id;
