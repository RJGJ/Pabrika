-- Project-wide feed. Every name carries the Feed prefix.

-- name: FeedList :many
-- Newest first by (created_at, id) over two sources: ticket_activity (kind 'activity', or
-- 'decision' for the decision action) and live comments (kind 'comment', body cut to 200
-- characters). Tickets and comments are soft-deleted, so both are filtered explicitly.
SELECT f.id, f.kind, f.ticket_id, f.ticket_number, f.ticket_title, f.actor_type, f.actor_id,
       f.verb, f.changes, f.summary, f.created_at,
       u.display_name AS user_name, tk.name AS token_name, ou.display_name AS owner_name
FROM (
  SELECT a.id AS id,
         CAST(CASE WHEN a.action = 'decision' THEN 'decision' ELSE 'activity' END AS TEXT) AS kind,
         a.ticket_id AS ticket_id, t.number AS ticket_number, t.title AS ticket_title,
         a.actor_type AS actor_type, a.actor_id AS actor_id, a.action AS verb,
         a.changes AS changes, CAST('' AS TEXT) AS summary, a.created_at AS created_at
  FROM ticket_activity a JOIN tickets t ON t.id = a.ticket_id
  WHERE t.project_id = sqlc.arg(project_id) AND t.deleted_at IS NULL
  UNION ALL
  SELECT c.id AS id, CAST('comment' AS TEXT) AS kind,
         c.ticket_id AS ticket_id, t.number AS ticket_number, t.title AS ticket_title,
         c.author_type AS actor_type, c.author_id AS actor_id, CAST('' AS TEXT) AS verb,
         CAST('{}' AS TEXT) AS changes, CAST(substr(c.body, 1, 200) AS TEXT) AS summary, c.created_at AS created_at
  FROM comments c JOIN tickets t ON t.id = c.ticket_id
  WHERE t.project_id = sqlc.arg(project_id) AND t.deleted_at IS NULL AND c.deleted_at IS NULL
) f
LEFT JOIN users u ON f.actor_type = 'user' AND u.id = f.actor_id
LEFT JOIN api_tokens tk ON f.actor_type = 'api_token' AND tk.id = f.actor_id
LEFT JOIN users ou ON ou.id = tk.user_id
WHERE (CAST(sqlc.narg(kind) AS TEXT) IS NULL OR f.kind = CAST(sqlc.narg(kind) AS TEXT))
  AND (CAST(sqlc.narg(actor_type) AS TEXT) IS NULL OR f.actor_type = CAST(sqlc.narg(actor_type) AS TEXT))
  AND (CAST(sqlc.narg(ticket_id) AS TEXT) IS NULL OR f.ticket_id = CAST(sqlc.narg(ticket_id) AS TEXT))
  AND (CAST(sqlc.narg(since) AS TEXT) IS NULL OR f.created_at > CAST(sqlc.narg(since) AS TEXT))
  AND (CAST(sqlc.arg(has_cursor) AS INTEGER) = 0
       OR f.created_at < CAST(sqlc.arg(cursor_created_at) AS TEXT)
       OR (f.created_at = CAST(sqlc.arg(cursor_created_at) AS TEXT) AND f.id < CAST(sqlc.arg(cursor_id) AS TEXT)))
ORDER BY f.created_at DESC, f.id DESC
LIMIT sqlc.arg(row_limit);
