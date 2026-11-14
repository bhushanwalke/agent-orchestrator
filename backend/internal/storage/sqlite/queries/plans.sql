-- Task plans: a plan row, its milestones, the milestone dependency edges, and
-- an append-only event log. CDC triggers on these tables emit 'plan_updated'.
-- The (plan_id, key) UNIQUE constraint surfaces as
-- domain.ErrMilestoneKeyExists in the store.

-- name: InsertPlan :exec
INSERT INTO plans (
    id, project_id, title, brief, ticket_ref, spec_markdown, state,
    orchestrator_session_id, max_parallel, created_at, updated_at, activated_at, finished_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: SelectPlanByID :one
SELECT * FROM plans WHERE id = ?;

-- name: SelectPlansByProject :many
SELECT * FROM plans WHERE project_id = ? ORDER BY created_at DESC, id DESC;

-- name: SelectAllPlans :many
SELECT * FROM plans ORDER BY created_at DESC, id DESC;

-- name: SelectActivePlans :many
SELECT * FROM plans WHERE state = 'active' ORDER BY created_at, id;

-- name: UpdatePlan :execrows
UPDATE plans
SET title = ?, brief = ?, ticket_ref = ?, spec_markdown = ?, state = ?,
    orchestrator_session_id = ?, max_parallel = ?, updated_at = ?,
    activated_at = ?, finished_at = ?
WHERE id = ?;

-- name: InsertMilestone :exec
INSERT INTO plan_milestones (
    id, plan_id, key, ordinal, kind, title, description, status, session_id,
    sparse_paths, qa_verdict, qa_note, attempt, created_at, updated_at, started_at, finished_at,
    retry_base, branch
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateMilestone :execrows
UPDATE plan_milestones
SET key = ?, ordinal = ?, kind = ?, title = ?, description = ?, status = ?,
    session_id = ?, sparse_paths = ?, qa_verdict = ?, qa_note = ?, attempt = ?,
    updated_at = ?, started_at = ?, finished_at = ?, retry_base = ?, branch = ?
WHERE id = ?;

-- name: DeleteMilestone :execrows
DELETE FROM plan_milestones WHERE id = ?;

-- name: SelectMilestonesByPlan :many
SELECT * FROM plan_milestones WHERE plan_id = ? ORDER BY ordinal, id;

-- name: SelectMilestoneBySession :one
-- A milestone's session_id names its current owner; an earlier owner (a
-- replaced QA session) no longer matches.
SELECT * FROM plan_milestones
WHERE session_id = ? AND session_id <> ''
ORDER BY updated_at DESC, id DESC
LIMIT 1;

-- name: SelectMilestoneDepKeysByPlan :many
SELECT d.milestone_id, dep.key AS depends_on_key
FROM plan_milestone_deps d
JOIN plan_milestones m ON m.id = d.milestone_id
JOIN plan_milestones dep ON dep.id = d.depends_on_id
WHERE m.plan_id = ?
ORDER BY dep.ordinal, dep.id;

-- name: DeleteMilestoneDeps :exec
DELETE FROM plan_milestone_deps WHERE milestone_id = ?;

-- name: InsertMilestoneDep :exec
INSERT INTO plan_milestone_deps (milestone_id, depends_on_id) VALUES (?, ?);

-- name: InsertPlanEvent :one
INSERT INTO plan_events (plan_id, milestone_id, session_id, kind, note, created_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: SelectPlanEventsAfter :many
SELECT * FROM plan_events
WHERE plan_id = ? AND id > ?
ORDER BY id
LIMIT ?;

-- name: SelectRecentPlanEvents :many
SELECT * FROM plan_events
WHERE plan_id = ?
ORDER BY id DESC
LIMIT ?;

-- name: DeletePlan :execrows
DELETE FROM plans WHERE id = ?;

-- name: SelectPlanAttachedSessionIDs :many
-- Sessions attached to a plan without a milestone: research and
-- investigation workers the orchestrator spawned with `ao spawn --plan`.
SELECT id FROM sessions
WHERE plan_id = ? AND plan_milestone_id = ''
ORDER BY created_at, id;
