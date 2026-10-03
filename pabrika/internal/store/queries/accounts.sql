-- name: AccountGetPasswordHash :one
SELECT password_hash FROM users WHERE id = ?;

-- name: AccountGetCredentialsByEmail :one
-- Login lookup. users.email is COLLATE NOCASE, so the match is case-insensitive.
SELECT id, password_hash FROM users WHERE email = ?;
