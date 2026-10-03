-- name: SessionGetByHash :one
-- One session joined with its user. Expiry is judged by the caller (it owns the clock).
SELECT s.token_hash, s.user_id, s.expires_at, s.created_at,
       u.email, u.display_name, u.created_at AS user_created_at
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = ?;

-- name: SessionExtend :execrows
UPDATE sessions SET expires_at = ? WHERE token_hash = ?;

-- name: SessionDeleteByHash :execrows
DELETE FROM sessions WHERE token_hash = ?;

-- name: SessionDeleteAllForUser :execrows
DELETE FROM sessions WHERE user_id = ?;

-- name: SessionDeleteExpired :execrows
-- Timestamps are fixed-width text, so string comparison is chronological.
DELETE FROM sessions WHERE expires_at <= ?;
