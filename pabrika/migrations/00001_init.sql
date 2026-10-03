-- +goose Up
CREATE TABLE users (
  id            TEXT PRIMARY KEY,
  email         TEXT NOT NULL UNIQUE COLLATE NOCASE,
  display_name  TEXT NOT NULL,
  password_hash TEXT NOT NULL,
  created_at    TEXT NOT NULL
);

CREATE TABLE sessions (
  token_hash  TEXT PRIMARY KEY,
  user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at  TEXT NOT NULL,
  created_at  TEXT NOT NULL
);
CREATE INDEX sessions_by_user ON sessions (user_id);

CREATE TABLE projects (
  id                 TEXT PRIMARY KEY,
  key                TEXT NOT NULL UNIQUE,
  name               TEXT NOT NULL,
  description        TEXT NOT NULL DEFAULT '',
  next_ticket_number INTEGER NOT NULL DEFAULT 1,
  created_by         TEXT NOT NULL REFERENCES users(id),
  archived_at        TEXT,
  created_at         TEXT NOT NULL,
  updated_at         TEXT NOT NULL
);

CREATE TABLE project_members (
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role       TEXT NOT NULL CHECK (role IN ('owner','editor','viewer')),
  created_at TEXT NOT NULL,
  PRIMARY KEY (project_id, user_id)
);
CREATE INDEX members_by_user ON project_members (user_id);

CREATE TABLE api_tokens (
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
CREATE INDEX tokens_by_user ON api_tokens (user_id);

CREATE TABLE labels (
  id         TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name       TEXT NOT NULL COLLATE NOCASE,
  color      TEXT NOT NULL DEFAULT 'gray',
  UNIQUE (project_id, name)
);

CREATE TABLE tickets (
  id          TEXT PRIMARY KEY,
  project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  number      INTEGER NOT NULL,
  title       TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'todo'
              CHECK (status IN ('backlog','todo','in_progress','done')),
  priority    TEXT NOT NULL DEFAULT 'medium'
              CHECK (priority IN ('low','medium','high','urgent')),
  assignee_id TEXT REFERENCES users(id) ON DELETE SET NULL,
  position    REAL NOT NULL,
  due_date    TEXT,
  deleted_at  TEXT,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL,
  UNIQUE (project_id, number)
);
CREATE INDEX tickets_board ON tickets (project_id, status, position)
  WHERE deleted_at IS NULL;

CREATE TABLE ticket_labels (
  ticket_id TEXT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  label_id  TEXT NOT NULL REFERENCES labels(id) ON DELETE CASCADE,
  PRIMARY KEY (ticket_id, label_id)
);

CREATE TABLE comments (
  id          TEXT PRIMARY KEY,
  ticket_id   TEXT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  author_type TEXT NOT NULL CHECK (author_type IN ('user','api_token')),
  author_id   TEXT NOT NULL,
  body        TEXT NOT NULL,
  created_at  TEXT NOT NULL,
  edited_at   TEXT,
  deleted_at  TEXT
);
CREATE INDEX comments_by_ticket ON comments (ticket_id, created_at);

CREATE TABLE ticket_activity (
  id         TEXT PRIMARY KEY,
  ticket_id  TEXT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  actor_type TEXT NOT NULL CHECK (actor_type IN ('user','api_token')),
  actor_id   TEXT NOT NULL,
  action     TEXT NOT NULL,
  changes    TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);
CREATE INDEX activity_by_ticket ON ticket_activity (ticket_id, created_at, id);

-- +goose Down
DROP TABLE ticket_activity;
DROP TABLE comments;
DROP TABLE ticket_labels;
DROP TABLE tickets;
DROP TABLE labels;
DROP TABLE api_tokens;
DROP TABLE project_members;
DROP TABLE projects;
DROP TABLE sessions;
DROP TABLE users;
