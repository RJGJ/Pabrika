-- name: TokenGetByHash :one
-- API token by the SHA-256 of its secret, with owner and (optional) limited project key.
SELECT t.id, t.user_id, t.name, t.token_prefix, t.scope, t.project_id, t.last_used_at, t.revoked_at, t.created_at,
       t.token_hash,
       u.email AS owner_email, u.display_name AS owner_display_name, u.created_at AS owner_created_at,
       p.key AS project_key
FROM api_tokens t
JOIN users u ON u.id = t.user_id
LEFT JOIN projects p ON p.id = t.project_id
WHERE t.token_hash = ?;

-- name: TokenListForUser :many
-- Newest first, revoked included.
SELECT t.id, t.user_id, t.name, t.token_prefix, t.scope, t.project_id, t.last_used_at, t.revoked_at, t.created_at,
       p.key AS project_key
FROM api_tokens t
LEFT JOIN projects p ON p.id = t.project_id
WHERE t.user_id = ?
ORDER BY t.created_at DESC, t.id DESC;

-- name: TokenGetForUser :one
SELECT t.id, t.user_id, t.name, t.token_prefix, t.scope, t.project_id, t.last_used_at, t.revoked_at, t.created_at,
       p.key AS project_key
FROM api_tokens t
LEFT JOIN projects p ON p.id = t.project_id
WHERE t.id = ? AND t.user_id = ?;

-- name: TokenRevoke :execrows
-- Sets revoked_at only when still NULL. Rows affected is 1 for an existing own token even
-- when it was already revoked (idempotent), 0 for unknown or someone else's.
UPDATE api_tokens SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ? AND user_id = ?;

-- name: TokenTouch :execrows
-- Stamps last_used_at only when never used or last used before the threshold (now minus 60 s).
UPDATE api_tokens SET last_used_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND (last_used_at IS NULL OR last_used_at < sqlc.arg(threshold));

-- name: TokenCountActive :one
SELECT COUNT(*) FROM api_tokens WHERE user_id = ? AND revoked_at IS NULL;
