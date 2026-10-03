-- Comment queries. Every name carries the Comment prefix.

-- name: CommentInsert :exec
INSERT INTO comments (id, ticket_id, author_type, author_id, body, created_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: CommentGetView :one
-- A live comment with resolved author names (LEFT JOINs: missing rows give NULL names).
SELECT c.id, c.ticket_id, c.author_type, c.author_id, c.body, c.created_at, c.edited_at,
       u.display_name AS user_name, tk.name AS token_name, ou.display_name AS owner_name
FROM comments c
LEFT JOIN users u ON c.author_type = 'user' AND u.id = c.author_id
LEFT JOIN api_tokens tk ON c.author_type = 'api_token' AND tk.id = c.author_id
LEFT JOIN users ou ON ou.id = tk.user_id
WHERE c.id = ? AND c.deleted_at IS NULL;

-- name: CommentListAsc :many
-- Oldest first by (created_at, id), excluding deleted; keyset in expanded OR form.
SELECT c.id, c.ticket_id, c.author_type, c.author_id, c.body, c.created_at, c.edited_at,
       u.display_name AS user_name, tk.name AS token_name, ou.display_name AS owner_name
FROM comments c
LEFT JOIN users u ON c.author_type = 'user' AND u.id = c.author_id
LEFT JOIN api_tokens tk ON c.author_type = 'api_token' AND tk.id = c.author_id
LEFT JOIN users ou ON ou.id = tk.user_id
WHERE c.ticket_id = sqlc.arg(ticket_id) AND c.deleted_at IS NULL
  AND (CAST(sqlc.arg(has_cursor) AS INTEGER) = 0
       OR c.created_at > CAST(sqlc.arg(cursor_created_at) AS TEXT)
       OR (c.created_at = CAST(sqlc.arg(cursor_created_at) AS TEXT) AND c.id > CAST(sqlc.arg(cursor_id) AS TEXT)))
ORDER BY c.created_at, c.id
LIMIT sqlc.arg(row_limit);

-- name: CommentListNewest :many
-- Newest first (the caller flips to oldest first); used by Latest(n).
SELECT c.id, c.ticket_id, c.author_type, c.author_id, c.body, c.created_at, c.edited_at,
       u.display_name AS user_name, tk.name AS token_name, ou.display_name AS owner_name
FROM comments c
LEFT JOIN users u ON c.author_type = 'user' AND u.id = c.author_id
LEFT JOIN api_tokens tk ON c.author_type = 'api_token' AND tk.id = c.author_id
LEFT JOIN users ou ON ou.id = tk.user_id
WHERE c.ticket_id = sqlc.arg(ticket_id) AND c.deleted_at IS NULL
ORDER BY c.created_at DESC, c.id DESC
LIMIT sqlc.arg(row_limit);

-- name: CommentUpdateBody :exec
UPDATE comments SET body = ?, edited_at = ? WHERE id = ?;

-- name: CommentSoftDelete :exec
UPDATE comments SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL;

-- name: CommentSeedDeleteToken :exec
-- Test only: remove a token row so author names fall back to "deleted token".
DELETE FROM api_tokens WHERE id = ?;

-- name: CommentSeedDeleteUser :exec
-- Test only: remove a user row so author names fall back to "deleted user".
DELETE FROM users WHERE id = ?;

-- name: CommentSeedGetRaw :one
-- Test only: a comment row including soft-deleted ones.
SELECT * FROM comments WHERE id = ?;

-- name: CommentSeedSetRole :exec
-- Test only: change a member's role directly (e.g. demote a comment author).
UPDATE project_members SET role = ? WHERE project_id = ? AND user_id = ?;
