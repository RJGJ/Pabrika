-- name: CreateUser :one
INSERT INTO users (id, email, display_name, password_hash, created_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = ?;

-- name: ListUsersByIDs :many
SELECT * FROM users WHERE id IN (sqlc.slice('ids'));

-- name: UpdateUserDisplayName :one
UPDATE users SET display_name = ? WHERE id = ?
RETURNING *;

-- name: UpdateUserPassword :execrows
UPDATE users SET password_hash = ? WHERE id = ?;

-- name: CreateSession :exec
INSERT INTO sessions (token_hash, user_id, expires_at, created_at)
VALUES (?, ?, ?, ?);

-- name: ListSessionHashesForUser :many
SELECT token_hash FROM sessions WHERE user_id = ? ORDER BY token_hash;

-- name: DeleteSessionsForUserExcept :execrows
DELETE FROM sessions WHERE user_id = ? AND token_hash <> ?;

-- name: CreateAPIToken :one
INSERT INTO api_tokens (id, user_id, name, token_hash, token_prefix, scope, project_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetAPIToken :one
SELECT * FROM api_tokens WHERE id = ?;
