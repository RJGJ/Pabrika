-- name: LabelList :many
SELECT id, project_id, name, color FROM labels WHERE project_id = ? ORDER BY name COLLATE NOCASE, id;

-- name: LabelGet :one
SELECT id, project_id, name, color FROM labels WHERE id = ?;

-- name: LabelNameTaken :one
-- True when another label (id <> exclude_id; pass '' for none) of the project has the name (NOCASE column).
SELECT EXISTS (
  SELECT 1 FROM labels
  WHERE project_id = sqlc.arg(project_id) AND name = sqlc.arg(name) AND id <> sqlc.arg(exclude_id)
);

-- name: LabelInsert :one
INSERT INTO labels (id, project_id, name, color) VALUES (?, ?, ?, ?)
RETURNING id, project_id, name, color;

-- name: LabelUpdate :one
UPDATE labels SET name = ?, color = ? WHERE id = ?
RETURNING id, project_id, name, color;

-- name: LabelDelete :exec
DELETE FROM labels WHERE id = ?;
