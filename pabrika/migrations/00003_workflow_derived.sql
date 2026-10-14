-- +goose Up
-- Derived workflow cache on tickets (parsed from the description and bot comments by the service,
-- never authoritative) and the insert-only record of human gate decisions.
ALTER TABLE tickets ADD COLUMN pr_url TEXT;
ALTER TABLE tickets ADD COLUMN branch TEXT;
ALTER TABLE tickets ADD COLUMN round_n INTEGER;
ALTER TABLE tickets ADD COLUMN round_max INTEGER;

-- actor_user_id is a plain column (no foreign key), like comments.author_id: the audit record
-- outlives the account and resolves to "deleted user".
CREATE TABLE ticket_decisions (
  id            TEXT PRIMARY KEY,
  ticket_id     TEXT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  project_id    TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  gate          TEXT NOT NULL CHECK (gate IN ('requirements','plan')),
  verdict       TEXT NOT NULL CHECK (verdict IN ('approved','changes_requested')),
  note          TEXT,
  actor_user_id TEXT NOT NULL,
  actor_role    TEXT NOT NULL CHECK (actor_role IN ('owner','editor')),
  gate_label    TEXT NOT NULL,
  created_at    TEXT NOT NULL
);
CREATE INDEX decisions_by_ticket ON ticket_decisions (ticket_id, gate, created_at DESC, id);
CREATE INDEX decisions_by_project ON ticket_decisions (project_id, created_at, id);

-- +goose Down
DROP TABLE ticket_decisions;
ALTER TABLE tickets DROP COLUMN round_max;
ALTER TABLE tickets DROP COLUMN round_n;
ALTER TABLE tickets DROP COLUMN branch;
ALTER TABLE tickets DROP COLUMN pr_url;
