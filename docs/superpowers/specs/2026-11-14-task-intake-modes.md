# Task intake modes: direct, plan, auto

Addendum to `2026-11-14-task-plans-design.md`. Builds on branch `feat/task-plans` at `43f1559821`.

## 1. Goal

Today a Task is created one of two ways: the **Task** tab spawns a single worker session with the prompt (direct), the **Plan** tab creates a plan that the project orchestrator researches, splits into milestones and runs through QA. A person has to pick. Recurring Automations can only spawn sessions.

Add a third mode, **auto**: the orchestrator reads the brief first and decides whether it needs a plan or a single worker. Expose the three modes everywhere a Task can be created, so an automation can say "auto" and get the right shape of work each run.

Mode names, used verbatim in the API, CLI, config and stored rows: `direct`, `plan`, `auto`.

## 2. Behaviour

### 2.1 Direct (unchanged)

The Task tab. One worker session, no plan row. Nothing in this addendum changes it.

### 2.2 Plan (unchanged)

The Plan tab. A plan row in state `planning`, kickoff `<ao-plan-request>`, the existing flow.

### 2.3 Auto

1. Creating an auto Task writes a plan row with `intake_mode = 'auto'` and persisted state `triage`.
2. Kickoff delivers `<ao-plan-triage id="PLAN">` to the orchestrator instead of `<ao-plan-request>`. The body carries everything the plan request carries (title, brief, ticket, project, the plan id) plus the decision instructions, so the orchestrator can continue planning in the same turn without another message. Deferred-kickoff handling (`kickoff_waiting`, `kickoff_sent`, `RetryDeferredKickoff`) is reused as is.
3. The orchestrator decides with `ao plan triage PLAN --plan --reason "<one sentence>"` or `ao plan triage PLAN --direct --reason "<one sentence>"`.
   - `--plan`: state `triage` → `planning`, event `triage_decided {decision: plan, reason, decidedBy: orchestrator}`. From here the plan is an ordinary plan; the orchestrator proceeds with steps 1 to 6 of the Plans section of its prompt.
   - `--direct`: the service spawns **one plain worker session** exactly as the Task tab would: project worker role (agent, model, permissions), `Prompt` = the composed brief (§2.4), `DisplayName` = the Task title (explicit), no `plan_id`, no `plan_milestone_key`, no `AO_PLAN_ID`. The plan row moves to persisted state `direct` with `direct_session_id` set and `finished_at` set. Events `triage_decided {decision: direct, …}` then `direct_spawned {sessionId}`. The worker then behaves like any Task-tab worker: its own board card, reports to the orchestrator through the report coordinator, PR flow, reviews. The plan is terminal and the plan scheduler ignores it.
   - If the direct spawn fails, the plan stays in `triage`, event `direct_spawn_failed {error}`, and the orchestrator gets `<ao-plan-triage-failed id=PLAN>` with the error so it can retry the decision or cancel.
4. Reminder: if a plan has been in `triage` for 15 minutes since `kickoff_sent` with no decision, the scheduler sends `<ao-plan-triage-reminder id="PLAN">` once (event `triage_reminder`). No automatic decision is ever made.
5. Humans can decide too: `POST /plans/{id}/triage` from the run view Actions menu ("Plan it", "Send to one worker") or the CLI; `decidedBy: human`. `ao plan cancel` works in `triage` as it does in `planning`.

### 2.4 Composed brief for a direct worker

```
Task: <title>

<brief>

Ticket: <ticketRef>        (line omitted when empty)
```

This is also the text the Plan composer would have shown the orchestrator, so direct and plan workers see the same words.

### 2.5 Triage rule in the orchestrator prompt

Add to `orchestratorPlanPrompt`, before the existing Plans steps:

> When you receive `<ao-plan-triage id="PLAN">`, the human (or an automation) created a Task and asked you to decide how to run it. Decide in this turn, after a short look at the code if the brief leaves it unclear. Choose **direct** when the work is one coherent change a careful engineer finishes in a few hours as one PR, touches one area with its own tests, and needs no research or QA pass over several parts. Choose **plan** when the work splits into parts that can proceed in parallel, needs investigation before it can be specified, crosses more than one area (for example API and UI), or needs a QA agent to verify the whole. Record the decision with `ao plan triage PLAN --direct --reason "..."` or `ao plan triage PLAN --plan --reason "..."`. After `--plan`, continue with the Plans steps below in the same turn. After `--direct`, AO spawns one worker with the brief; you are done until it reports.

Project `orchestratorRules` can bias the choice (for example "prefer direct for docs-only tickets"); say so in the guide.

## 3. Data model

New migration (do not modify shipped ones):

```sql
ALTER TABLE plans ADD COLUMN intake_mode TEXT NOT NULL DEFAULT 'plan' CHECK (intake_mode IN ('plan','auto'));
ALTER TABLE plans ADD COLUMN direct_session_id TEXT NOT NULL DEFAULT '';
ALTER TABLE plans ADD COLUMN triage_decision TEXT NOT NULL DEFAULT '' CHECK (triage_decision IN ('','plan','direct'));
ALTER TABLE plans ADD COLUMN triage_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE plans ADD COLUMN triage_decided_by TEXT NOT NULL DEFAULT '' CHECK (triage_decided_by IN ('','orchestrator','human'));
ALTER TABLE plans ADD COLUMN triage_decided_at TIMESTAMP;
ALTER TABLE plans ADD COLUMN triage_reminded_at TIMESTAMP;
```

`plans.state` gains values `triage` and `direct`. If the state column has a CHECK constraint, the migration recreates it to allow them (SQLite cannot alter a CHECK in place; use the rebuild pattern already used elsewhere in this repo's migrations, or confirm there is no CHECK and say so).

Automations:

```sql
ALTER TABLE automations ADD COLUMN task_mode TEXT NOT NULL DEFAULT 'direct' CHECK (task_mode IN ('direct','plan','auto'));
ALTER TABLE automation_runs ADD COLUMN plan_id TEXT REFERENCES plans (id) ON DELETE SET NULL;
```

Both migrations need `Down` sections consistent with the file's neighbours and entries in the burned-versions test.

## 4. Domain

- `PlanState` adds `PlanStateTriage = "triage"` and `PlanStateDirect = "direct"`.
- `PlanIntakeMode` type: `plan`, `auto`. `TriageDecision`: `plan`, `direct`. `TriageActor`: `orchestrator`, `human`.
- `Plan` gains `IntakeMode`, `DirectSessionID`, `Triage *PlanTriage{Decision, Reason, DecidedBy, DecidedAt}`, and `TriageRemindedAt *time.Time`.
- `DerivePlanPhase`: state `triage` → phase `triage`; state `direct` → phase `direct`. Both new phases are added to `PlanPhase`. `direct` is terminal (treated like `done` for "finished plans are not ticked").
- Plan kanban (board) column: `triage` maps to the same column as `planning`. Phase `direct` plans are **not** listed as Task cards (the worker session card stands for the work); the plan list API still returns them.
- Validation: triage decision is rejected with 409 `PLAN_NOT_IN_TRIAGE` unless state is `triage`; `reason` max 500 bytes, 400 `INVALID_TRIAGE_REASON`; decision must be `plan` or `direct`, 400 `INVALID_TRIAGE_DECISION`.

## 5. Service

`service/plan`:

- `CreateInput.Mode PlanIntakeMode` (zero value = plan). `auto` creates the row in `triage` and the kickoff message is `<ao-plan-triage>`.
- `Triage(ctx, planID, decision, reason, actor) (Plan, error)` implements §2.3. The direct spawn goes through the same session spawn boundary the plan service already uses for milestone workers (`ports.SpawnConfig` via the session service), with `PlanSpawnContext` **nil** and `SparsePaths` empty (a direct worker gets a full checkout, like a Task-tab worker). Idempotency: a second `Triage` on a plan already decided returns 409 `PLAN_NOT_IN_TRIAGE` and does not spawn again.
- Scheduler `Tick`: plans in `triage` are swept for the 15-minute reminder (§2.3.4) and for deferred kickoff retry, nothing else. Plans in `direct` are never ticked.
- `planTriageMessage(plan, project)` in `prompts.go`, next to the plan request message, with the same title/brief/ticket block, and `planTriageReminderMessage`, `planTriageFailedMessage`.
- Orchestrator notice tags: `<ao-plan-triage id=…>`, `<ao-plan-triage-reminder id=…>`, `<ao-plan-triage-failed id=…>`.
- Events (kinds added to the allowed list): `triage_requested` (on create), `triage_decided`, `triage_reminder`, `direct_spawned`, `direct_spawn_failed`.

`service/automation`:

- `CreateInput`/`UpdateInput` gain `TaskMode` (default `direct`). `kind=orchestrator` with `task_mode != direct` → 400 `INVALID_AUTOMATION_TASK_MODE` ("An orchestrator automation runs a session, not a Task plan").
- Scheduler `dispatch`: `direct` → existing spawn path, unchanged. `plan` / `auto` → create a plan through a new `PlanCreator` dependency (interface with `Create(ctx, plan.CreateInput) (domain.Plan, error)`, injected like `SessionSpawner`; nil → run fails with "plan service is unavailable"), with `Title = "<displayName> · <scheduledFor formatted 2006-01-02 15:04 in the automation's timezone>"` clipped to the plan title limit, `Brief = prompt`, `Mode` from the automation, `MaxParallel` nil. The run is marked running with `plan_id` set and `session_id` null.
- Run completion for plan-backed runs: `Reconcile`/`Tick` completes a run when its plan reaches phase `done` (status `completed`), `failed`/`cancelled` (status `failed`, error message = phase), or `direct` (then the run adopts `direct_session_id` as its `session_id` and completes/fails the way a session-backed run does today). Pick the smallest change that reuses the existing session-backed completion code path; say which.
- `AutomationRun` gains `PlanID *PlanID` (`json:"planId,omitempty"`); `Automation` gains `TaskMode` (`json:"taskMode"`).

## 6. API

- `CreatePlanRequest.mode?: "plan" | "auto"` (default `plan`; `direct` is rejected with 400 `INVALID_PLAN_MODE`, since direct Tasks are sessions, not plans).
- `POST /plans/{planId}/triage` body `{decision: "plan" | "direct", reason?: string}` → 200 `PlanResponse`. Errors per §4. Human-initiated calls record `decidedBy: human`; the CLI run from inside an orchestrator session (AO_SESSION_ID of the plan's orchestrator) records `orchestrator`. Use the same session-identity mechanism `ao plan activate` uses today to tell them apart; if there is none, add an `X-AO-Session` style header only if one already exists in the codebase, otherwise accept `decidedBy` from the CLI flag `--by` defaulting to `orchestrator` when `AO_SESSION_ID` is set and `human` otherwise.
- `PlanSummary` / `PlanResponse` gain `intakeMode`, `phase` adds `triage` and `direct`, `directSessionId?`, `triage?: {decision, reason, decidedBy, decidedAt}`.
- Automations: `taskMode` on create/update/get/list; `planId` on run rows.
- Regenerate apispec and the frontend schema; `npm run api` must show no diff afterwards.

## 7. CLI

- `ao plan create --mode plan|auto` (default plan).
- `ao plan triage [<plan-id>] (--plan | --direct) [--reason <text>]`. Exactly one of `--plan`/`--direct`, else usage exit 2. Prints the resulting phase and, for direct, the session id.
- `ao automation create|update --task-mode direct|plan|auto`.
- `ao automation runs` prints `plan` alongside `session` when set.
- Update `skillassets/using-ao/commands/plan.md` (triage) and the automation command doc.

## 8. Desktop app

### 8.1 New task dialog (`NewChatPage.tsx`)

Three tabs, in this order: **Task**, **Plan**, **Auto**. i18n keys `newTask.plan.modeTask` (exists), `newTask.plan.modePlan` (exists), `newTask.plan.modeAuto` = "Auto". `setNewTaskMode` accepts `"task" | "plan" | "auto"` (ui-store). Plan and Auto both render `PlanComposer` with a `mode` prop; Auto's submit button reads "Create task" (`newTask.plan.submitAuto`) and a one-line helper under the form reads "The orchestrator reads the brief and decides whether to plan it or hand it to one worker." (`newTask.plan.autoHint`). Translate the new keys in the 8 locales.

### 8.2 Run view

- Phase `triage`: header badge "Deciding" (neutral tone), graph area shows a single centred line "Waiting for the orchestrator to decide how to run this Task" with the orchestrator card in the rail; Actions gains "Plan it" and "Send to one worker" (both call `POST /plans/{id}/triage`; confirm the second with the existing dialog pattern used for Cancel). Events render the new kinds in sentence case: "Triage requested", "Decided: plan — <reason>", "Decided: one worker — <reason>", "Reminder sent to the orchestrator", "Worker started", "Worker could not start: <error>".
- Phase `direct`: header badge "One worker", the graph area shows "Handled by a single worker" with the triage reason and an "Open worker session" button (navigates to the session route). No milestones, no legend. The Actions menu keeps "Open orchestrator" and "Show spec" (spec is empty; hide if empty).
- Board: `triage` plans appear as Task cards in the planning column with the "Deciding" badge; `direct` plans do not appear as Task cards (§4).

### 8.3 Automations

In the create/edit dialog, when role is worker, add a "Run as" select (shadcn `Select`) with options "One worker" (`direct`), "Task plan" (`plan`), "Let the orchestrator decide" (`auto`); default "One worker". Hidden for the orchestrator role, which sends `direct`. The automation card and the runs list show the mode and, for plan-backed runs, a link to the Task run view instead of a session link. i18n keys under `automations.taskMode.*`, translated in the 8 locales.

## 9. Tests

- Domain: `DerivePlanPhase` for `triage` and `direct`; kanban mapping; `direct` excluded from board Task cards.
- Service plan: auto create → state `triage`, kickoff tag `<ao-plan-triage`, event `triage_requested`; `Triage(plan)` → state planning + event; `Triage(direct)` → spawn called once with the composed brief, explicit display name, nil plan context, no sparse paths, plan state `direct`, `direct_session_id`, events; second `Triage` → 409; spawn failure → state stays `triage`, event `direct_spawn_failed`, failed notice sent; reminder sent once at 15 minutes and not again; `direct` plans never ticked.
- Controller: create with `mode` (plan, auto, direct→400, unknown→400), triage route success/409/400s, response fields.
- CLI: `ao plan triage` flag validation (exit 2), success output; `ao plan create --mode`; automation `--task-mode`.
- Automation scheduler: `plan`/`auto` dispatch calls `PlanCreator` with the expected title/brief/mode and records `plan_id`; run completion on plan done/failed/cancelled/direct; nil creator fails the run; orchestrator + non-direct rejected.
- Migrations: burned-versions entries; fresh and existing data apply.
- Frontend: three tabs render and switch the composer; Auto submit posts `mode: "auto"`; run view triage and direct states; Actions triage calls; AutomationsView select and payload; events copy.

## 10. Docs

Update `frontend/src/docs/content/guides/task-plans.mdx` (the three modes, the triage rule, how to bias it), `configuration/lifecycle-automation.mdx` (Run as), `cli.mdx` and `docs/cli/README.md` (new flags and command), the handoff doc, and `2026-11-14-task-plans-design.md` (link this addendum).

## 11. Out of scope

Automatic triage decisions without an agent; per-project default mode; changing the Task tab.

## 12. Verification (ao-verify)

1. Migrations apply on a fresh dir and on `/tmp/ao-plans/e2e-data` (backup first).
2. App: New task shows Task / Plan / Auto; create an Auto Task on `ao-e2e-todo3` with a small brief (for example "Rename the 'Coming up' panel heading to 'Due soon' and update its test") and watch the orchestrator pick **direct**: plan phase `direct`, one worker card on the board with the Task title, no Task card, run view shows "Handled by a single worker" with the reason, the worker finishes and reports done.
3. Create an Auto Task with a broad brief (the TODO MVP brief on a fresh clone) and watch it pick **plan**, then run to QA passed as before.
4. Human override: create an Auto Task, use Actions → "Send to one worker" before the orchestrator answers (or after cancelling its controller), confirm `decidedBy: human`.
5. Reminder: hold the orchestrator (kill its controller) and confirm exactly one `triage_reminder` after 15 minutes, then recover.
6. Automation: create an automation with Run as "Let the orchestrator decide" on a one-minute schedule, confirm the run records `planId`, the Task appears, and the run completes when the plan finishes; then the same with "Task plan" and with "One worker". Orchestrator role + plan mode is rejected.
7. CLI: `ao plan create --mode auto`, `ao plan triage --direct` outside triage → 409, flag errors exit 2, `ao automation create --task-mode auto`.
8. Gates as before (Go packages touched, lint, typecheck, `npm run api` no diff, renderer vitest).

## 13. Implementation rulings

Decisions made while building this addendum where the text above left a choice.

- **R1 (state CHECK).** `plans.state` has a CHECK, so migration `20261114231812030_task_intake_modes.sql` widens it in place through `sqlite_schema` (the same guarded `writable_schema` edit the task plans migration uses for `change_log`) instead of rebuilding `plans`, which would drop its CDC triggers. Down maps `triage`/`direct` plans to `cancelled` before narrowing the CHECK back.
- **R2 (direct on the board).** `DerivePlanKanban(direct)` returns the archive column with display status "One worker"; `PlanPhase.ShowsTaskCard()` is false for `direct`, and the board uses it to skip the card. `triage` reads "Deciding" in the building lane. `PlanPhase.Finished()` is true for done, cancelled and direct.
- **R3 (who decided).** No session-identity header exists, so `POST /plans/{id}/triage` takes `decidedBy` (`orchestrator` | `human`, default `human`). `ao plan triage --by` defaults to `orchestrator` when `AO_SESSION_ID` is set and `human` otherwise.
- **R4 (single decision).** The store writes a decision with `DecidePlanTriage`, an `UPDATE … WHERE state = 'triage'` that reports whether it won, so two deciders can never both record one.
- **R5 (spawn once).** `Service.Triage` holds a dedicated `triageMu` across check, spawn and write, so concurrent decisions serialise and every later one gets 409 `PLAN_NOT_IN_TRIAGE` without spawning. The direct spawn also carries `ClientRequestID = "plan-direct:<planId>"`, so a replay returns the same session. The 409 message names the recorded decision ("already decided (plan, by human); continue with the Plans steps").
- **R6 (direct worker config).** The direct worker uses the Task tab's spawn (`session.DelegateTask`): `Kind=worker`, the project's worker role (no harness or model override), `Prompt` = §2.4 brief, `NameFromPrompt` for non-scratch projects, `Async`, plus `DisplayName` = title with `DisplayNameExplicit` and `KeepDisplayName` (the same pair the session service sets for any named CLI worker). There is no parent session, no `CreatedBy`, no plan context and no sparse paths.
- **R7 (human plan decision).** When a person chooses plan after the triage request landed, the orchestrator gets the ordinary `<ao-plan-request>` so it plans. If the request had not landed yet, the kickoff retry delivers a plan request, because the plan is now in `planning`. An orchestrator's own `--plan` sends nothing more, since it continues in the same turn.
- **R8 (spawn failure).** `Triage(direct)` returns the spawn error to the caller (its API kind is kept). The plan stays in `triage` with no decision recorded, `direct_spawn_failed` is logged, and `<ao-plan-triage-failed>` is sent best effort. The same decision can then be retried.
- **R9 (reminder).** The 15 minutes count from the `kickoff_sent` event. A reminder the orchestrator refuses is retried on the next tick, and `triage_reminded_at` is set only once delivery succeeds, so exactly one is delivered.
- **R10 (events).** `triage_decided` notes read "`<decision>`, decided by `<actor>`: `<reason>`" for `ao plan events`. The app reads the decision from `plan.triage`. `direct_spawned` carries the worker in `sessionId`. Creating an auto Task logs `plan_created` then `triage_requested`.
- **R11 (cancel).** `ao plan cancel` on a `direct` plan returns it unchanged: it is finished, and its worker is archived from its own card.
