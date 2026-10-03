-- name: GetProjectForUser :one
-- Resolves a project by id OR key together with the user's membership in one query.
-- Pass the ULID in ref_id (or "") and the uppercased key in ref_key (or "").
-- No row means unknown project and non-member alike.
SELECT p.id, p.key, p.name, p.description, p.next_ticket_number, p.created_by,
       p.archived_at, p.created_at, p.updated_at, m.role AS member_role
FROM projects p
JOIN project_members m ON m.project_id = p.id AND m.user_id = sqlc.arg(user_id)
WHERE (CAST(sqlc.arg(ref_id) AS TEXT) <> '' AND p.id = CAST(sqlc.arg(ref_id) AS TEXT))
   OR (CAST(sqlc.arg(ref_key) AS TEXT) <> '' AND p.key = CAST(sqlc.arg(ref_key) AS TEXT));

-- name: LocateTicketByID :one
-- Owning project of a live (not soft-deleted) ticket.
SELECT t.id, t.project_id, t.number, p.key AS project_key
FROM tickets t JOIN projects p ON p.id = t.project_id
WHERE t.id = ? AND t.deleted_at IS NULL;

-- name: LocateTicketByRef :one
-- Same, by project key (uppercase) and ticket number.
SELECT t.id, t.project_id, t.number, p.key AS project_key
FROM tickets t JOIN projects p ON p.id = t.project_id
WHERE p.key = ? AND t.number = ? AND t.deleted_at IS NULL;

-- name: LocateLabel :one
SELECT id, project_id FROM labels WHERE id = ?;

-- name: LocateComment :one
-- Owning ticket and project of a live comment on a live ticket.
SELECT c.id, c.ticket_id, c.author_type, c.author_id, t.project_id, t.number AS ticket_number, p.key AS project_key
FROM comments c
JOIN tickets t ON t.id = c.ticket_id
JOIN projects p ON p.id = t.project_id
WHERE c.id = ? AND c.deleted_at IS NULL AND t.deleted_at IS NULL;
