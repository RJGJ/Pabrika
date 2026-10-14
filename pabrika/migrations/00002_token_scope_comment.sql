-- +goose Up
-- SQLite cannot alter a CHECK constraint, so api_tokens is rebuilt to accept the 'comment' scope.
-- No table references api_tokens (comments and activity store actor_type/actor_id as plain
-- columns), so the rebuild needs no foreign_keys juggling.
CREATE TABLE api_tokens_new (
  id           TEXT PRIMARY KEY,
  user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  token_hash   TEXT NOT NULL UNIQUE,
  token_prefix TEXT NOT NULL,
  scope        TEXT NOT NULL CHECK (scope IN ('read','comment','write')),
  project_id   TEXT REFERENCES projects(id) ON DELETE CASCADE,
  last_used_at TEXT,
  revoked_at   TEXT,
  created_at   TEXT NOT NULL
);
INSERT INTO api_tokens_new (id, user_id, name, token_hash, token_prefix, scope, project_id, last_used_at, revoked_at, created_at)
SELECT id, user_id, name, token_hash, token_prefix, scope, project_id, last_used_at, revoked_at, created_at FROM api_tokens;
DROP TABLE api_tokens;
ALTER TABLE api_tokens_new RENAME TO api_tokens;
CREATE INDEX tokens_by_user ON api_tokens (user_id);

-- +goose Down
-- Least privilege: comment tokens become read tokens, then the old CHECK is restored.
UPDATE api_tokens SET scope = 'read' WHERE scope = 'comment';
CREATE TABLE api_tokens_old (
  id           TEXT PRIMARY KEY,
  user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  token_hash   TEXT NOT NULL UNIQUE,
  token_prefix TEXT NOT NULL,
  scope        TEXT NOT NULL CHECK (scope IN ('read','write')),
  project_id   TEXT REFERENCES projects(id) ON DELETE CASCADE,
  last_used_at TEXT,
  revoked_at   TEXT,
  created_at   TEXT NOT NULL
);
INSERT INTO api_tokens_old (id, user_id, name, token_hash, token_prefix, scope, project_id, last_used_at, revoked_at, created_at)
SELECT id, user_id, name, token_hash, token_prefix, scope, project_id, last_used_at, revoked_at, created_at FROM api_tokens;
DROP TABLE api_tokens;
ALTER TABLE api_tokens_old RENAME TO api_tokens;
CREATE INDEX tokens_by_user ON api_tokens (user_id);
