# Task Plans: ticket → plan → milestone workers → QA — Design Spec

Date: 2026-11-14. Repo: `agent-orchestrator` (Go daemon in `backend/`, Electron/React app in `frontend/`, shared UI in `packages/product-ui`). Work lands on branch `orchestrator-plus` in a git worktree; `main` is never edited directly.

This document is the source of truth for the feature. The companion plan `docs/superpowers/plans/2026-11-14-task-plans.md` splits it into testable milestones.

Addendum: `docs/superpowers/specs/2026-11-14-task-intake-modes.md` adds the direct / plan / auto intake modes (an auto Task the orchestrator triages into a plan or one worker) and the matching Automation task modes.

## 1. Goal

A person creates a **Task** in the desktop app by pasting a ticket. From then on AO behaves like a small engineering team:

1. The project **orchestrator** (strong model, Codex `astra` by default) investigates the repo, optionally spawns parallel **research workers**, writes a plan, and registers **milestones** with dependencies.
2. AO's **plan scheduler** spawns one **implementation worker** (cheaper model, Codex `sol` by default) per milestone, in parallel when milestones are independent and in dependency order otherwise, each in its own cone-mode sparse worktree. Workers build and run unit/integration tests before reporting done.
3. When every implementation milestone is done, the scheduler spawns a **QA worker** that merges all milestone branches locally, builds, tests, and exercises the whole feature. QA records a pass or a per-milestone fail with context.
4. A QA fail routes the defect back to the owning worker (same session if alive, otherwise a fresh worker on the same branch). QA re-runs after fixes. On pass the plan is done and the orchestrator reports to the human.
5. Implementation workers open the PRs (existing AO flow). QA never owns PRs.

The app shows Tasks on the board; a Task opens a run view with the milestone graph, agent cards, and recent events; an agent card opens the existing session view.

## 2. Vocabulary and naming

| UI word | Code word | Why |
|---|---|---|
| Task (board card) | `Plan` / `plan_id` | `Task` already means a speculative worktree token and the New Task dialog in code. |
| Milestone | `Milestone` | Same word. |
| Agent card | existing session | No new concept. |
| QA agent | worker session whose milestone has `kind = qa` | Avoids a new `SessionKind` (that enum appears in 38 files). |

Plan role of a session (`planner`, `implementer`, `qa`) is derived from its `plan_milestone_id` and the milestone kind; the orchestrator is the planner. No new `SessionKind`.

## 3. Data model (SQLite, new migration)

Create with `npm run migration:create -- task_plans`, add the generated id to `shippedMigrations` in `backend/internal/storage/sqlite/migrate_burned_versions_test.go`, and add queries under `backend/internal/storage/sqlite/queries/plans.sql`, then `npm run sqlc`.

```sql
CREATE TABLE plans (
    id                 TEXT PRIMARY KEY,                      -- "plan_" + uuid
    project_id         TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    title              TEXT NOT NULL,
    brief              TEXT NOT NULL,                         -- the ticket text as pasted
    ticket_ref         TEXT NOT NULL DEFAULT '',              -- URL or "github:owner/repo#12"; display only
    spec_markdown      TEXT NOT NULL DEFAULT '',              -- the orchestrator's plan document
    state              TEXT NOT NULL CHECK (state IN ('planning','active','cancelled')),
    orchestrator_session_id TEXT NOT NULL DEFAULT '',
    max_parallel       INTEGER NOT NULL DEFAULT 3 CHECK (max_parallel BETWEEN 1 AND 10),
    created_at         TIMESTAMP NOT NULL,
    updated_at         TIMESTAMP NOT NULL,
    activated_at       TIMESTAMP,
    finished_at        TIMESTAMP
);
CREATE INDEX plans_project_idx ON plans(project_id, created_at);

CREATE TABLE plan_milestones (
    id                 TEXT PRIMARY KEY,                      -- "ms_" + uuid
    plan_id            TEXT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    key                TEXT NOT NULL,                         -- short slug, e.g. "m1", "qa"; unique per plan
    ordinal            INTEGER NOT NULL,                      -- insertion order
    kind               TEXT NOT NULL CHECK (kind IN ('implement','qa')),
    title              TEXT NOT NULL,
    description        TEXT NOT NULL,                         -- the milestone's own spec, Markdown
    status             TEXT NOT NULL CHECK (status IN ('pending','ready','running','blocked','done','failed','cancelled')),
    session_id         TEXT NOT NULL DEFAULT '',              -- current owning session
    sparse_paths       TEXT NOT NULL DEFAULT '[]',            -- JSON array of repo-relative dirs
    qa_verdict         TEXT NOT NULL DEFAULT '' CHECK (qa_verdict IN ('','pass','fail')),
    qa_note            TEXT NOT NULL DEFAULT '',
    attempt            INTEGER NOT NULL DEFAULT 0,            -- spawns/fix rounds so far
    created_at         TIMESTAMP NOT NULL,
    updated_at         TIMESTAMP NOT NULL,
    started_at         TIMESTAMP,
    finished_at        TIMESTAMP,
    UNIQUE (plan_id, key)
);

CREATE TABLE plan_milestone_deps (
    milestone_id   TEXT NOT NULL REFERENCES plan_milestones(id) ON DELETE CASCADE,
    depends_on_id  TEXT NOT NULL REFERENCES plan_milestones(id) ON DELETE CASCADE,
    PRIMARY KEY (milestone_id, depends_on_id)
);

CREATE TABLE plan_events (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    plan_id       TEXT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    milestone_id  TEXT NOT NULL DEFAULT '',
    session_id    TEXT NOT NULL DEFAULT '',
    kind          TEXT NOT NULL,   -- see §9
    note          TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMP NOT NULL
);
CREATE INDEX plan_events_plan_idx ON plan_events(plan_id, id);

ALTER TABLE sessions ADD COLUMN plan_id TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN plan_milestone_id TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN sparse_paths TEXT NOT NULL DEFAULT '[]';
```

CDC: add triggers that insert `change_log` rows with `event_type = 'plan_updated'` and payload `json_object('id', plan_id)` after insert/update on `plans` and `plan_milestones` and after insert on `plan_events` (project_id joined from `plans`). Add `EventPlanUpdated EventType = "plan_updated"` to `backend/internal/cdc/event.go`. The SSE stream already forwards every change_log event; the frontend invalidates plan queries on it.

Never persist derived plan status. `state` is the only persisted plan lifecycle fact; everything the UI shows is derived (§5).

## 4. Domain types (`backend/internal/domain/plan.go`)

```go
type PlanID string
type MilestoneID string
type PlanState string        // planning | active | cancelled
type MilestoneKind string    // implement | qa
type MilestoneStatus string  // pending | ready | running | blocked | done | failed | cancelled
type QAVerdict string        // "" | pass | fail
type PlanPhase string        // derived: planning | implementing | qa | fixing | done | cancelled | failed

type Plan struct {
    ID PlanID; ProjectID ProjectID; Title, Brief, TicketRef, SpecMarkdown string
    State PlanState; OrchestratorSessionID SessionID; MaxParallel int
    CreatedAt, UpdatedAt time.Time; ActivatedAt, FinishedAt *time.Time
}
type Milestone struct {
    ID MilestoneID; PlanID PlanID; Key string; Ordinal int; Kind MilestoneKind
    Title, Description string; Status MilestoneStatus; SessionID SessionID
    SparsePaths []string; DependsOn []string /* keys */; QAVerdict QAVerdict; QANote string
    Attempt int; CreatedAt, UpdatedAt time.Time; StartedAt, FinishedAt *time.Time
}
type PlanEvent struct { ID int64; PlanID PlanID; MilestoneID MilestoneID; SessionID SessionID; Kind string; Note string; CreatedAt time.Time }

const MaxPlanMilestones = 20
const DefaultPlanMaxParallel = 3
const PlanBriefMaxBytes = 102400
const MilestoneDescriptionMaxBytes = 32768
const QANoteMaxBytes = 8192
```

Validation (`(Milestone).Validate`, `ValidatePlanGraph(ms []Milestone) error`):
- `key`: `^[a-z0-9][a-z0-9-]{0,31}$`; unique per plan; `qa` key is reserved for the QA milestone.
- Title 1–100 chars after trim; description non-empty for `implement`.
- Dependencies reference existing keys in the same plan; no self-dependency; graph acyclic (Kahn); a `qa` milestone may not be depended on and has implicit dependencies on every `implement` milestone.
- Sparse paths pass the existing `validateRepoRelative` rule; max 50 entries; `qa` milestones must have none (QA always gets a full checkout).
- Exactly one `qa` milestone at activation; if none was added, `Activate` inserts `{key: "qa", kind: qa, title: "QA: verify the whole feature", description: ""}` automatically.
- At least one `implement` milestone at activation.

Derivation (`DerivePlanPhase(plan Plan, ms []Milestone) PlanPhase` and `DerivePlanKanban(phase PlanPhase, ms []Milestone) (contract.KanbanColumn, contract.DisplayStatus)`):

| Condition (first match wins) | Phase | Kanban column | Display status |
|---|---|---|---|
| state = cancelled | cancelled | archive | Cancelled |
| state = planning | planning | building | Planning |
| any milestone blocked | implementing (or fixing/qa if applicable) | building | Blocked |
| QA milestone status = failed, or any implement milestone has qa_verdict = fail and status ∈ {ready, running} | fixing | building | Fixing QA findings |
| QA milestone status = running | qa | validating | Testing |
| QA milestone status = done and qa_verdict = pass | done | ready | QA passed |
| any implement milestone failed (not a QA fail) and attempt ≥ 3 | failed | needs_review | Needs you |
| otherwise | implementing | building | `Implementing k/n` where k = done implement milestones, n = total implement milestones |

Add `KanbanBlocked`‑style new `DisplayStatus` phrases to `backend/pkg/contract/kanban.go`: `Planning`, `Implementing`, `Fixing QA findings`, `Testing`, `QA passed`, `Cancelled`. They are only used by plans; the session enum tag in `domain.Session` is unchanged.

## 5. Service (`backend/internal/service/plan`)

Files: `service.go` (CRUD + activation + qa-result), `scheduler.go` (`Tick`), `prompts.go` (milestone/QA/kickoff prompt text), `store.go` (Store interface), `types.go`, tests beside each.

```go
type Deps struct {
    Store Store
    Sessions Sessions          // Spawn, Send, Get, EnsureOrchestrator (thin interface over service/session)
    Projects Projects          // Get(projectID)
    Now func() time.Time
    NewID func(prefix string) string
    Logger *slog.Logger
}
type Sessions interface {
    EnsureOrchestrator(ctx, projectID domain.ProjectID) (domain.SessionRecord, error)   // POST /orchestrators semantics, idempotent
    SpawnWorker(ctx, ports.SpawnConfig, domain.ProjectRecord) (domain.SessionRecord, error)
    Send(ctx, id domain.SessionID, text string, opts ports.MessageDeliveryOptions) error // guarded delivery
    Get(ctx, id domain.SessionID) (domain.SessionRecord, bool, error)
}
```

### 5.1 Create and kickoff

`Create(ctx, CreateInput{ProjectID, Title, Brief, TicketRef, MaxParallel}) (domain.Plan, error)` (attachments: not yet):
1. Validate (title 1–100, brief 1–102400 bytes, maxParallel 1–10 default 3).
2. Insert plan `state=planning`.
3. `EnsureOrchestrator(projectID)`; store its id on the plan.
4. Record event `plan_created`.
5. Send the orchestrator the kickoff message (below) via `Send` with `SenderSessionID=""`, `AuthoredByUser=false`. If the orchestrator cannot start or refuses delivery (busy, still starting), record `kickoff_deferred`; a background retry (every 5 s for about 5 minutes) keeps trying, and after that the scheduler tick's sweep delivers it. The plan stays `planning` and the UI shows "Waiting for the orchestrator".

Kickoff message (exact wrapper so the prompt hook and tests can recognise it):

```
<ao-plan-request id="{planId}">
A new Task was created for project {projectName}.

Title: {title}
Ticket: {ticketRef or "(none)"}

{brief}

Plan it now. Follow the "Plans (Tasks)" section of your standing instructions:
investigate the repository, spawn research workers in parallel if the ticket is broad
(attach each with `ao spawn --plan {planId} ...`, and kill them once their findings are in the plan),
write the plan with `ao plan set-spec {planId} --file <plan.md>`, register milestones with
`ao plan milestone add {planId} ...`, then `ao plan activate {planId}`.
</ao-plan-request>
```

### 5.2 Milestone editing (state = planning only)

`AddMilestone(ctx, planID, MilestoneInput{Key, Kind, Title, Description, DependsOn, SparsePaths})`, `UpdateMilestone(ctx, planID, key, MilestoneInput)`, `RemoveMilestone(ctx, planID, key)`, `SetSpec(ctx, planID, markdown)`, `Update(ctx, planID, PlanPatch{Title, MaxParallel, SpecMarkdown})`. All return `PLAN_NOT_EDITABLE` (409) when state ≠ planning, except `maxParallel`, which stays editable after activation (refused only for a cancelled plan). Codes: `PLAN_NOT_FOUND` (404), `MILESTONE_NOT_FOUND` (404), `INVALID_MILESTONE` (400, with `details.field`), `MILESTONE_KEY_EXISTS` (409), `PLAN_TOO_MANY_MILESTONES` (400).

### 5.3 Activate

`Activate(ctx, planID)`: `ValidatePlanGraph`; auto-add the QA milestone if missing; set every milestone `pending`; `state=active`, `activated_at=now`; event `plan_activated`; call `Tick` once synchronously so the first workers spawn before the response returns (bounded by the request budget; spawns themselves are `Async: true` Chat spawns, see §5.4). Errors: `PLAN_GRAPH_INVALID` (400, details list the problem), `PLAN_NOT_EDITABLE`.

### 5.4 Scheduler

`Tick(ctx, now time.Time) error` is called every 15 s by `observe/plans` (copy of `observe/automations` observer pattern) and immediately by `Wake()` from the report hook and from activation. For each `active` plan, in `created_at` order:

1. **Promote**: every `pending` milestone whose dependencies are all `done` becomes `ready` (event `milestone_ready`). A `qa` milestone's dependencies are all `implement` milestones.
2. **Reconcile running**: for each `running` or `blocked` milestone, read its session. Session terminated, activity `exited`, or provisioning failed without a done report → status `failed`, `attempt` unchanged, event `milestone_failed` with note `session {id} ended without reporting done`. While attempts used (`attempt - retry_base`) < 3, the next tick re-spawns it (it goes back to `ready`); at 3 it stays `failed` and the plan phase becomes `failed` (human needed). A live `running` milestone whose Chat thread is in `system_error`, or whose last two turns failed, becomes `blocked` with event `milestone_blocked` "agent turns are failing: <reason>"; its next `checkpoint` or `done` sets it `running` again.
3. **Spawn**: count `running` implement milestones; while count < `max_parallel`, take the next `ready` implement milestone (by ordinal) and spawn it (§6). For `qa`: spawn when `ready` regardless of the cap. Each spawn: `attempt++`, `status=running`, `session_id`, `started_at`, event `milestone_started`. A spawn error → `status=failed` with the error note; retried next tick up to 3 attempts.
4. **Finish**: when the QA milestone is `done` with verdict `pass`, set `finished_at`, event `plan_done`, and send the orchestrator `<ao-plan-done id="…">` summary. Its first line after the verdict is `QA branch: <branch>`: the QA worktree's checked-out branch (every milestone merged and verified), else the QA session's recorded branch marked `(unresolved: …)`, else `QA branch: (unresolved; ask the QA session …)`. Then the QA note, each milestone's title, session id, branch, and PR URLs from report outputs, and an instruction to hand the human the QA branch as the verified combined result with the milestone branches and PR links beneath it. The plan stays `active` (state) and reads as phase `done`.

Idempotency: every spawn carries `ClientRequestID = "plan:" + milestoneID + ":" + attempt` so a crashed tick cannot double-spawn (existing replay logic). `attempt` only grows; a human retry sets `retry_base = attempt` (a fresh budget of 3) instead of resetting `attempt`, so a retried spawn never reuses an earlier request id.

On daemon start the observer ticks every active plan, so an upgraded or restarted daemon resumes scheduling (including spawning milestones that became ready while it was down).

### 5.5 Report hook

The report service already exposes `onCreated(domain.ReportRecord)`. Wire a second callback into the plan service, `OnReport(ctx, rec)`, which finds the milestone by `rec.SessionID` (via `sessions.plan_milestone_id`) and:

| Report | Effect |
|---|---|
| `done` from an implement milestone | status `done`, `finished_at`, the milestone's work branch stored in `plan_milestones.branch`, event `milestone_done` (note = report note, clipped to 900 bytes at the last sentence end, else a word; workers are asked to lead with what they verified). `Wake()`. The branch is resolved at done time, outside the service lock: `ao report --branch <name>`, else the worktree's checked-out branch (`git symbolic-ref --short HEAD`, 5 s), else the head branch of a PR the report created, else the session's recorded branch with event `milestone_branch_unresolved`. Dependents and QA merge the stored branch. |
| `done` from the QA milestone | after a `pass` verdict: nothing more (the plan finishes on the next tick). After per-milestone `fail` verdicts: QA returns to `pending` (event `milestone_done` "QA round finished with failures") and is re-promoted once every failed milestone is `done` again. After a `fail` no milestone owns: stays `blocked`. With no verdict: status `failed` with note "QA finished without a verdict", and the scheduler sends the QA session a message asking for `ao plan qa-result` (event `qa_verdict_requested`) until it lands; the session may still record its verdict while in that state. |
| `stuck` or `needs_input` | status `blocked`, event `milestone_blocked`. Status returns to `running` on the session's next `checkpoint` or `done`. |
| `checkpoint` | event `milestone_checkpoint` only. |

### 5.6 QA results and the fix loop

`RecordQAResult(ctx, planID, QAResultInput{Verdict, MilestoneKey, Note, SessionID})` (CLI `ao plan qa-result`; only the QA milestone's own session may call it, else `PLAN_QA_SESSION_REQUIRED` 403):

- `pass` (no milestone key): QA milestone `done`, `qa_verdict=pass`, event `qa_passed`. The plan finishes on the next tick.
- `fail` with `MilestoneKey` of an implement milestone: that milestone gets `qa_verdict=fail`, `qa_note=note`, status `ready` (if its session is alive and not terminated the scheduler **reuses it**: sends `<ao-qa-defect plan="…" milestone="…">{note}</ao-qa-defect>` with the fix instructions from §6.3 and sets status `running` without spawning; if the session is terminated it spawns a fresh worker with `Branch` = the milestone's stored work branch and `BaseBranch` empty, so the fix continues on the same branch). Event `qa_failed`. QA stays `running` so it can record findings against other milestones (a `pass` is refused once the round has failures, `PLAN_QA_HAS_FAILURES`); its `done` report returns it to `pending`, and it is re-promoted once every failed milestone is `done` again (QA attempt++ → a new QA session; the previous QA session is left idle, not killed). Only the current round's QA session may record (`PLAN_QA_NOT_RUNNING` otherwise).
- `fail` with no key: the QA milestone becomes `blocked` with the note and the orchestrator receives the note (human triage).

### 5.6a Orchestrator notices

A milestone that becomes `blocked` (a `stuck` or `needs_input` report, or failing agent turns, §5.4) or `failed` with no attempts left needs a decision the scheduler cannot make. Each tick delivers every owed notice to the plan's orchestrator through the guarded `Send`, retrying next tick while it is refused:

- `<ao-milestone-blocked plan="…" milestone="…" session="…">{block note}\n\nDecide: clarify the task with `ao send --session <id> --message …`, or `ao plan cancel`, or tell the human.</ao-milestone-blocked>`; event `milestone_blocked_notified`.
- `<ao-milestone-failed plan="…" milestone="…" session="…">` with the last failure note and the retry command; event `milestone_failed_notified`.

One notice per episode: a notice is owed while the milestone's latest `milestone_blocked` (or exhausting `milestone_failed`) event is newer than its latest matching `*_notified` event. A block that resolves before delivery is still delivered (it is informational), saying the milestone is running again; its event note says so. A terminated orchestrator closes the episode with the note "the orchestrator session has ended; not delivered".

### 5.7 Cancel

`Cancel(ctx, planID)`: state `cancelled`, every non-terminal milestone `cancelled`, event `plan_cancelled`. Sessions are **not** killed (the user may still want them); the run view offers per-agent archive through the existing card menu.

## 6. Spawning milestone workers

`ports.SpawnConfig` gets:

```go
// PlanContext links a spawn to a plan milestone. Nil for ordinary spawns.
PlanContext *PlanSpawnContext
// SparsePaths requests a cone-mode sparse checkout containing only these
// repo-relative directories (root files are always present). Empty is a full checkout.
SparsePaths []string
```
```go
type PlanSpawnContext struct {
    PlanID domain.PlanID; MilestoneID domain.MilestoneID; MilestoneKey string
    Role string            // "implementer" | "qa"
    PromptSection string   // built by service/plan.prompts.go, appended to the worker system prompt
}
```

The session manager persists `plan_id`, `plan_milestone_id`, `sparse_paths` on the session row and exports `AO_PLAN_ID` and `AO_MILESTONE_KEY` into the agent environment next to `AO_SESSION_ID`.

**Research workers.** `ao spawn --plan <planId>` (`planId` on `POST /sessions`) attaches an ordinary worker to a plan without a milestone: `PlanSpawnContext{PlanID}` only, so the row has `plan_id` set and `plan_milestone_id = ''`, and the agent gets `AO_PLAN_ID` but no `AO_MILESTONE_KEY`. The sessions controller checks the plan through `PlanService.Lookup`: unknown → 404 `PLAN_NOT_FOUND`; a plan of another project, or no project → 400 `PLAN_PROJECT_MISMATCH`; `kind=orchestrator` → 400 `PLAN_ATTACH_WORKER_ONLY`. The CLI rejects `--plan` with `--standalone` or `--kind orchestrator` (exit 2). The service never kills these sessions; the orchestrator does once their findings are in the plan. `GET /plans/{id}` lists them as `researchSessions` (index `sessions_plan_idx` on `sessions(plan_id)`).

Spawn parameters per milestone:

| Field | implement | qa |
|---|---|---|
| Kind | worker | worker |
| Harness / model / effort | project `worker` override (unchanged) | project `worker` override |
| ParentSessionID | plan.OrchestratorSessionID | same |
| CreatedBy | orchestrator | orchestrator |
| DisplayName | `{plan.Title} · {milestone.Key}: {milestone.Title}` truncated to 100 | `{plan.Title} · QA` |
| Prompt | §6.1 | §6.2 |
| BaseBranch | empty (default branch). Exactly-one-dependency milestones use the dependency's branch instead, so their PR stacks on it. | empty |
| SparsePaths | milestone.SparsePaths ∪ project `sparseCheckout.alwaysInclude` | none |
| RequestedMode | daemon default (Chat preferred) | same |
| Async | true | true |

### 6.1 Implementation worker prompt (user prompt, Markdown)

```
# Milestone {key}: {title}
Part of Task "{plan.Title}" (plan {planId}). Ticket: {ticketRef}

## Your milestone
{description}

## Dependencies already done
- {depKey}: {depTitle} — branch `{depBranch}` (session {depSessionId}). Merge it into your branch before you start: `git merge {depBranch}`.
(or "None.")

## Rules for this milestone
- Build the project and run the unit and integration tests that cover your change before you report done. Fix failures yourself.
- Commit on your branch. Open the pull request as the project rules allow (the task was created by the user in the app, so publishing is authorized), and attach it with `ao report --done --pr-created <url>`.
- Report progress with `ao report --checkpoint`, blockers with `ao report --stuck`, and completion with `ao report --done --note "<what you built, how you verified it>"`. The plan scheduler waits for your done report.
- Do not work on other milestones. If you find the milestone cannot be completed as specified, `ao report --stuck` with the reason.
```
Plus, when `SparsePaths` is non-empty, the sparse section from §7.

### 6.2 QA worker prompt

```
# QA for Task "{plan.Title}" (plan {planId})
Ticket: {ticketRef}

## What was built
- {key}: {title} — branch `{branch}`, PRs: {urls or "none"}; summary: {done report note}
...

## Your job
1. Merge every milestone branch above into your branch, in the order listed (`git merge <branch>`). Resolve trivial conflicts; if a conflict is substantive, record a fail for the milestone that caused it.
2. Build the project, run the full test suite, and exercise the feature end to end as a user would. Use `ao preview` and `ao browser` when available; if they report AGENT_BROWSER_NOT_INSTALLED or the Browser panel is unavailable, exercise the UI with your own headless browser or HTTP checks and say which you used. Compare the behaviour against the ticket and the plan spec:
{spec_markdown, clipped to 8000 chars}
3. For each defect, record it against the milestone whose change caused it:
   `ao plan qa-result {planId} --fail --milestone <key> --note "<steps to reproduce, expected, actual, file hints>"`
   One command per milestone; put all defects for a milestone in one note.
4. If everything passes: `ao plan qa-result {planId} --pass --note "<what you verified>"`.
5. Finish with `ao report --done --note "<summary>"`. Do not open pull requests and do not push.
```

### 6.3 QA defect delivery to an implementation worker

```
<ao-qa-defect plan="{planId}" milestone="{key}">
QA found problems with your milestone "{title}":

{note}

Fix them on your current branch, rebuild, rerun the relevant tests, push the fix to your PR if one exists, then `ao report --done --note "<what you changed>"`. QA will run again after all fixes are in.
</ao-qa-defect>
```

### 6.4 Orchestrator prompt additions (`session_manager/prompt.go`, new `orchestratorPlanPrompt()` section appended to `orchestratorSystemPrompt`)

```
## Plans (Tasks)

When you receive a message wrapped in <ao-plan-request id="PLAN">, the human created a Task in the app and you are its planner. Do this, in order:

1. Investigate: read the ticket, inspect the relevant code, tests, and docs. You may read anything; you still must not edit code.
2. If the ticket is broad, spawn research workers in parallel with `ao spawn --project {projectId} --plan PLAN --name "Research: <topic>" --output artifact --prompt "<question>"` and wait for their `done` reports before planning. Prefer two to four focused questions over one vague one. `--plan PLAN` (the id from <ao-plan-request>) attaches each one to the Task, so it shows on the Task's run view. Once their findings are in the plan, end each research worker with `ao session kill <id>` so it does not linger on the board.
3. Write the plan as Markdown: goal, approach, the milestone list with what each one changes and how it is verified, risks. Save it with `ao plan set-spec PLAN --file <path>`.
4. Register milestones: `ao plan milestone add PLAN --key m1 --title "<title>" --description-file <file> [--depends-on m0,m2] [--sparse src/api --sparse src/shared]`. Keep milestones manageable: each should be one PR a careful engineer finishes in a few hours; split anything larger, merge anything trivial into a neighbour. Declare a dependency only when one milestone needs another's code. Use `--sparse` when the repository has more than roughly 200 tracked files or is a monorepo, listing the directories the milestone edits plus those it must read or build; for small repositories leave it off (QA always gets a full checkout).
5. Activate with `ao plan activate PLAN`. AO then spawns implementation workers (in parallel up to the plan's limit), then a QA worker, routes QA failures back to workers, and tells you with <ao-plan-done> when QA passes. Watch `ao plan get PLAN` and worker reports; intervene with `ao send` only when a worker is stuck or the plan needs changing.
6. When you receive <ao-plan-done>, hand the human the QA branch it names as the verified combined result (every milestone merged and tested by QA), then list the milestone branches and PR links beneath it. Do not present a milestone branch as the combined result.

When you receive <ao-milestone-blocked>, act on it in that turn: clarify the task for that worker with `ao send`, cancel the plan, or tell the human what decision is needed. When you receive <ao-milestone-failed>, act on it in that turn: find the cause, then retry it with `ao plan milestone retry PLAN <key>`, cancel the plan, or tell the human.

Never implement milestones yourself. Never activate a plan without at least one implement milestone and a description for each.
```

## 7. Cone-mode sparse worktrees

### 7.1 Project config
`domain.ProjectConfig` gets:
```go
// SparseCheckout controls cone-mode sparse worktrees for milestone workers.
SparseCheckout SparseCheckoutConfig `json:"sparseCheckout,omitempty"`
type SparseCheckoutConfig struct {
    // Enabled lets plan milestones and `ao spawn --sparse` create sparse worktrees. Default false.
    Enabled bool `json:"enabled,omitempty"`
    // AlwaysInclude are repo-relative directories added to every sparse checkout (shared libs, tooling).
    AlwaysInclude []string `json:"alwaysInclude,omitempty"`
}
```
Validation: each `AlwaysInclude` path passes `validateRepoRelative`; max 50. When `Enabled` is false, a spawn with `SparsePaths` fails with `SPARSE_CHECKOUT_DISABLED` (400) and the plan scheduler spawns a full checkout instead and records event `sparse_disabled` once per plan.

### 7.2 Workspace adapter (`adapters/workspace/gitworktree`)
`ports.WorkspaceConfig` gets `SparsePaths []string`. In `Create`, when non-empty and the project is an ordinary repository (not Scratch, not a multi-repo workspace: those return `ErrSparseUnsupported`):

```
git -C <repo> worktree add --no-checkout -b <branch> <path> <baseRef>
git -C <path> sparse-checkout init --cone
git -C <path> sparse-checkout set <paths...>
git -C <path> read-tree -mu HEAD
```
`existingWorktree` restore paths keep the existing behaviour; the recorded `sparse_paths` on the session row is informational for restore and the UI. `discard`, `remove`, dirty detection, and fork seeding are unchanged; add a test that `forkseed.Apply` into a sparse worktree copies only files inside the cone and widens the cone first with `git sparse-checkout add` for files outside it.

Failure handling: if any sparse command fails, remove the half-created worktree and return `ErrSparseCheckoutFailed` wrapping the git stderr; the session manager surfaces it as `WORKSPACE_SPARSE_FAILED` (500).

### 7.3 Prompt section (appended to any worker whose spawn had SparsePaths)
```
## Sparse checkout
This worktree is a cone-mode sparse checkout. Present directories: {paths}. Root-level files are always present. If you need another directory, run `git sparse-checkout add <dir>` (repeatable) before reading or editing it. Build and test commands may need directories outside the cone; add them rather than working around missing files.
```

### 7.4 CLI
`ao spawn --sparse <dir>` (repeatable, `StringArrayVar`). Rejected with a usage error together with `--standalone`. Added to the `using-ao` skill `spawn.md`. `ao spawn --plan <planId>` is documented next to it (§6, research workers).

## 8. HTTP API (code-first; edit `controllers/dto.go` and `apispec/specgen/build.go`, then `npm run api`)

Controller `controllers/plans.go` (`PlansController{Svc PlanService}`), registered in the router next to cues. All under `/api/v1`.

| Method & path | Body → Response | Notes |
|---|---|---|
| `POST /projects/{projectId}/plans` | `CreatePlanRequest{title, brief, ticketRef?, maxParallel?}` → 201 `PlanEnvelope` | Kicks off planning. Not yet: `attachments` (written into the orchestrator's worktree and listed in the kickoff) and `clientRequestId` idempotency; the New Task attach button is disabled in Plan mode. |
| `GET /projects/{projectId}/plans` | → `ListPlansResponse{plans: []PlanSummary}` | Newest first. |
| `GET /plans/{planId}` | → `PlanDetailResponse{plan, milestones, researchSessions, events}` | `events` = last 100. `researchSessions`: sessions attached with `ao spawn --plan` (no milestone), oldest first, each a `PlanSessionSummary` plus `branch`; `[]` when none. |
| `PATCH /plans/{planId}` | `PlanPatchRequest{title?, maxParallel?, specMarkdown?}` → `PlanEnvelope` | planning only, except `maxParallel` which is always editable. |
| `POST /plans/{planId}/milestones` | `MilestoneRequest{key, kind, title, description, dependsOn[], sparsePaths[]}` → 201 `MilestoneEnvelope` | |
| `PATCH /plans/{planId}/milestones/{key}` | `MilestoneRequest` (all optional) → `MilestoneEnvelope` | |
| `DELETE /plans/{planId}/milestones/{key}` | → 204 | |
| `POST /plans/{planId}/activate` | → `PlanDetailResponse` | |
| `POST /plans/{planId}/cancel` | → `PlanEnvelope` | |
| `POST /plans/{planId}/qa-result` | `QAResultRequest{verdict: pass|fail, milestoneKey?, note, sessionId}` → `PlanDetailResponse` | `sessionId` comes from `AO_SESSION_ID`. Another session: 403 `PLAN_QA_SESSION_REQUIRED`; no current QA round: 409 `PLAN_QA_NOT_RUNNING`; pass after a fail this round: 409 `PLAN_QA_HAS_FAILURES`. |
| `POST /plans/{planId}/milestones/{key}/retry` | → `PlanDetailResponse` | Human action: sets a `failed` milestone, a `blocked` qa milestone, or (Re-run QA) a `done` qa milestone once every implement milestone is done back to `ready` with a fresh budget (`retry_base = attempt`). Re-running a passed QA clears its verdict and the plan's `finished_at`. |
| `GET /plans/{planId}/events?after={id}&limit=` | → `PlanEventsResponse{events, nextAfter}` | |

DTO shapes (names registered in `schemaNames` as `ControllersPlan…`):

```go
type PlanSummary struct {
    ID string; ProjectID string; Title, TicketRef string; State string `enum:"planning,active,cancelled"`
    Phase string `enum:"planning,implementing,qa,fixing,done,cancelled,failed"`
    KanbanColumn string `enum:"building,validating,needs_review,ready,archive"`
    DisplayStatus string
    MilestoneCounts struct{ Total, Done, Running, Failed, Blocked int }
    LiveSessions []PlanSessionSummary   // id, displayName, harness, status, kanbanColumn, displayStatus
    OrchestratorSessionID string; MaxParallel int
    CreatedAt, UpdatedAt string; ActivatedAt, FinishedAt *string
}
type PlanResponse struct { PlanSummary; Brief, SpecMarkdown string }
type MilestoneResponse struct {
    ID, Key string; Ordinal int; Kind string `enum:"implement,qa"`; Title, Description string
    Status string `enum:"pending,ready,running,blocked,done,failed,cancelled"`
    DependsOn []string; SparsePaths []string; QAVerdict string `enum:",pass,fail"`; QANote string; Attempt int
    Session *PlanSessionSummary; Branch string; PRs []SessionPRFacts
    CreatedAt, UpdatedAt string; StartedAt, FinishedAt *string
}
type PlanEventResponse struct { ID int64; MilestoneKey, SessionID, Kind, Note, CreatedAt string }
```

`PlanSessionSummary` is built with the same read model as `GET /sessions` (status, kanbanColumn, displayStatus, harness, displayName) so the agent cards render identically to the board.

Error envelopes follow `apierr` conventions; request ids preserved.

## 9. Plan event kinds

`plan_created`, `kickoff_sent`, `kickoff_waiting` (a just-started orchestrator is still coming up), `kickoff_deferred` (the orchestrator could not start or refused), `spec_set`, `milestone_added`, `milestone_updated`, `milestone_removed`, `plan_activated`, `milestone_ready`, `milestone_started`, `milestone_checkpoint`, `milestone_blocked`, `milestone_done`, `milestone_failed`, `qa_passed`, `qa_failed`, `defect_sent`, `plan_done`, `plan_cancelled`, `sparse_disabled`, `milestone_branch_unresolved`, `qa_verdict_requested`, `plan_done_notified`, `milestone_blocked_notified`, `milestone_failed_notified`.

## 10. CLI (`backend/internal/cli/plan.go`, table-tested like `cue_test.go`)

```
ao plan create --project <id> --title <t> (--brief <text> | --brief-file <path>) [--ticket <ref>] [--max-parallel N]
ao plan ls [--project <id>] [--json]
ao plan get <plan-id> [--json]                # prints phase, milestones table (key, kind, status, session, deps), last events
ao plan set-spec <plan-id> --file <path>
ao plan milestone add <plan-id> --key k --title t (--description <text> | --description-file <path>) [--kind implement|qa] [--depends-on a,b] [--sparse dir]...   # --sparse is repeated, one directory per flag
ao plan milestone update <plan-id> <key> [same flags]
ao plan milestone rm <plan-id> <key>
ao plan milestone retry <plan-id> <key>
ao plan activate <plan-id>
ao plan cancel <plan-id> [--yes]
ao plan qa-result <plan-id> (--pass | --fail --milestone <key>) --note <text>   # sessionId from AO_SESSION_ID
ao plan events <plan-id> [--after N]
```
`<plan-id>` may be omitted inside a plan session (falls back to `AO_PLAN_ID`). Usage errors exit 2; daemon errors exit 1 with the envelope message. Mirror DTOs in the CLI; do not import controllers. Add `backend/internal/skillassets/using-ao/commands/plan.md` and a row in `SKILL.md`.

## 11. Desktop app

### 11.1 Creating a Task
`TaskComposer`/`NewTaskDialog` get a segmented control at the top of the brief: **Task** (today's behaviour) | **Plan** (new). In Plan mode the agent/model/mode pickers are hidden (the orchestrator plans with the project's orchestrator settings), a one-line **Title** field appears above the brief, an optional **Ticket link** field below it, and the primary button reads **Plan with orchestrator**. Submit calls `POST /projects/{id}/plans` and navigates to `/projects/$projectId/plans/$planId`. Local projects only (cloud projects keep Task only). Remember the last mode per project in `localStorage` key `ao.newTask.mode.<projectId>`.

### 11.2 Board
`SessionsBoard` gets plan cards. Data: `usePlansQuery(projectId)` on `GET /projects/{id}/plans`, invalidated by SSE `plan_updated` and `session_updated`. Rules:

- A plan renders as one card in the lane given by its `kanbanColumn`, using `SessionCardView` with: title = plan title; avatar strip = up to 4 `AgentAvatar`s of `liveSessions` (harness icons) and a `+n` overflow; status line = `displayStatus`; metadata = `MilestoneCounts.Done/Total` (implementation milestones only; QA is conveyed by the phase) as a compact metric (checklist icon and "3/5", "3 of 5 milestones done" in its title and accessible label, so the status phrase keeps its width) and `updatedAt`. A plan card has no branch line and no PR progress footer; PR chips show the union of milestone PRs (max 3, then `+n`).
- Sessions whose `planId` is set, milestone workers and research workers alike, are **not** rendered as their own board cards (they live inside the plan). A board toolbar toggle **Show agent sessions** (persisted in `ui-store`) reveals them again.
- Clicking a plan card navigates to the run view. The card's context menu offers: Open orchestrator, Cancel plan (confirm dialog), Copy plan id.
- Lane headers count plans + visible sessions.

### 11.3 Run view: `/projects/$projectId/plans/$planId`
New route file `_shell.projects.$projectId_.plans.$planId.tsx` rendering `PlanRunView` inside `CenterPanelShell` with the page topbar. Layout: center column (graph) and the existing right inspector rail width (320px) for agents and events; below 900px the rail stacks under the graph.

Header (page topbar): back to board; title; phase chip (semantic status colours from tokens, sentence case); "Started {relative} · {n} agent(s) active" (an i18next plural on `count`: "1 agent active", "2 agents active"; {n} counts live sessions whose agent activity is active, so a finished Task reads 0); ticket link if any; **Actions** menu: Open orchestrator chat, Cancel plan, Re-run QA (enabled when phase = done or fixing; retries the qa milestone, see the retry route in §8; while QA is still pending behind fixes the daemon refuses it with `MILESTONE_NOT_RETRYABLE` and the view shows the message), Show spec (opens the spec Markdown in a sheet using `ChatMarkdown`).

Graph (`PlanGraph.tsx`, pure SVG, no new dependency):
- Layered layout: longest-path layering from sources; nodes in a layer spaced 96px vertically, layers 220px apart, centered; edges as cubic curves from node right to node left; QA node always last layer.
- Node: 40px circle with the status glyph (check = done, spinner ring = running, dot = pending/ready, pause glyph = blocked, cross = failed, dash = cancelled) using the semantic colours already used by `getSessionStatusDotView`; label under the node: milestone title (truncate 24 chars with tooltip) and a muted second line: duration while running/done, "Waiting for m1, m2" while pending, agent name while running.
- Click selects the node and highlights its agent card; double-click or Enter opens the session when one exists. Keyboard: arrow keys move selection in layer order. No zoom, pan, or minimap.
- Legend row under the graph: Done, Running, Pending, Blocked, Failed.

Right rail: **Agents** section lists one compact `SessionCardView` per milestone that has a session (reusing `BoardSessionCardAdapter` in `compact` mode, same context menu). The orchestrator appears first with an "Orchestrator" label. Clicking a card navigates to the existing session route. Below it, a **Research** section (shown only when there are any) lists the plan's `researchSessions` that are still on the board, one compact card each; a killed research worker drops off. **Recent events** section: last 20 `plan_events` newest first as rows (glyph, "{milestone title} {verb}", relative time); "View all" expands to 100.

Live updates: SSE `plan_updated` and `session_updated` invalidate `['plan', planId]` and `['plans', projectId]`.

### 11.4 i18n
All copy in `frontend/src/renderer/i18n/en.json` under keys `plan.*`, `board.plans.*`, `newTask.plan.*`; other locales get the English string (the repo's convention for untranslated keys is to mirror `en.json`; follow whatever `all-messages.ts` enforces).

### 11.5 Design guide compliance
Follow `DESIGN.md`: no glows or gradients; lanes stay a shared grid; plan cards keep the quiet card border; one semantic colour per state; sentence case; the graph canvas is a plain surface, not a bordered panel; no metric tiles (counts live in the header line).

## 12. Tests required

Backend:
- `domain/plan_test.go`: key regex, graph validation (cycle, missing dep, qa depended on), phase derivation table.
- `service/plan/service_test.go`: create → kickoff message sent to ensured orchestrator; editing refused after activation; activation auto-adds QA; qa-result authorization.
- `service/plan/scheduler_test.go` with fakes: parallel cap (3 ready, cap 2 → 2 spawns), dependency ordering (m3 waits for m1 and m2), done report promotes dependents, QA spawns after all implement done, QA fail reuses a live session vs. spawns on the same branch for a terminated one, QA pass finishes and notifies the orchestrator, crash-safe idempotent spawn ids, failed-session retry to 3 attempts.
- `storage/sqlite/store/plan_store_test.go`: round trips, cascade delete, change_log rows written by the triggers.
- `httpd/controllers/plans_test.go`: every route happy path + validation + error envelopes; spec drift tests pass after `npm run api`.
- `cli/plan_test.go`: table tests mirroring `cue_test.go` (missing args exit 2, daemon error envelope exit 1, `AO_PLAN_ID` fallback, `--json`).
- `gitworktree/workspace_integration_test.go`: sparse create produces `git sparse-checkout list` equal to the requested cone; files outside the cone are absent; root files present; failure path removes the worktree.
- `session_manager`: prompt builder tests for the plan sections and `AO_PLAN_ID` env.

Frontend (vitest): `PlanGraph.test.tsx` (layout layers, status glyphs, keyboard selection), `PlanRunView.test.tsx` (agent card click navigates to session route; events render), `SessionsBoard.plans.test.tsx` (plan card placement, folding of plan sessions, toggle), `TaskComposer.plan.test.tsx` (Plan mode posts to the plans route and navigates).

Gates before each milestone handoff: `cd backend && go build ./... && go test ./...`, `npm run lint`, `npm run api` (no diff), `npm run frontend:typecheck`, `cd frontend && npx vitest run <touched>`.

## 13. Out of scope

Cloud projects, mobile app, Scratch and multi-repo workspace projects (plans are refused with `PLAN_PROJECT_KIND_UNSUPPORTED` 400), merging PRs automatically, new notification kinds, GitHub issue mirroring of plan state.
