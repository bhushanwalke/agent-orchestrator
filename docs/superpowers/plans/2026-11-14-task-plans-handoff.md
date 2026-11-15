# Task Plans: branch handoff

Branch `feat/task-plans` (worktree `/Users/ceo/ao-task-plans`), cut from `main` at `c40128f149`. Not pushed; no PR. Design spec: [`docs/superpowers/specs/2026-11-14-task-plans-design.md`](../specs/2026-11-14-task-plans-design.md). Implementation plan: [`2026-11-14-task-plans.md`](2026-11-14-task-plans.md). User guide: `frontend/src/docs/content/guides/task-plans.mdx`.

## What the branch adds

A **Task** (internally a *plan*) turns one ticket into coordinated work. You create it in the desktop app (New Task, **Plan** mode) or with `ao plan create`. The project orchestrator receives an `<ao-plan-request>`, investigates the repository (optionally fanning out research workers), writes a plan document, registers **milestones** with dependencies, and activates the plan. A daemon-side scheduler then spawns one worker session per milestone as its dependencies finish, in parallel up to `maxParallel`, optionally in a cone-mode **sparse worktree**. Each worker merges its dependencies' branches, builds, tests, and reports done; AO records the branch the work is on at that moment. When every milestone is done, a **QA worker** merges all milestone branches, tests the whole feature, and records `pass` or per-milestone `fail` verdicts; failures go back to the owning worker (the live session or a fresh worker on the same branch) and QA re-runs after the fixes. A pass finishes the Task and the orchestrator reports to the human. Failed sessions are retried up to 3 attempts; a worker that is stuck, needs input, or whose agent turns keep failing is marked blocked, and the orchestrator is told about every block and every milestone that runs out of attempts. In the app, Tasks appear as one card on the board (their worker sessions folded behind **Show agent sessions**) and open into a run view with a milestone graph, the agents, recent events, and Re-run QA / Cancel / Show spec actions.

## Commits by milestone

**Spec and plan**
- `5920b9d4fe` docs(plans): task plans design spec and implementation plan
- `c361839ec8` docs(plans): carry coordinator notes from M1 into the plan

**M1: plan core (data, domain, service, API, CLI)**
- `aeb3e27b06` feat(plans): add plan tables, store, and CDC events
- `07ff339353` feat(plans): plan domain types, graph validation, phase derivation
- `c8666bd292` feat(plans): plan service with planning lifecycle
- `5d66547fc9` feat(plans): plan HTTP API and OpenAPI
- `7892202f98` feat(cli): ao plan commands
- `b3d9d32229` fix(plans): keep session metadata comparable by storing sparse paths on the record
- `fdd736b04c` fix(plans): gate QA verdicts on a running QA round and return derived plan state
- `e643b7e5b8` chore(plans): satisfy golangci-lint (Patch name, builtin shadowing)
- `abe8d48dce` fix(plans): reject explicit qa dependencies and derive them on read
- `92293b3483` fix(plans): reject explicit maxParallel 0, unknown body fields; title event notes; finish cancelled plans

**M2: sparse worktrees**
- `d3df4a9fb4` feat(workspace): sparse checkout config and spawn plumbing
- `dde57ca1b6` feat(workspace): cone-mode sparse worktrees
- `b23e7bcf63` feat(prompts): explain sparse checkouts to workers
- `3ab2dcfa3f` chore(workspace): satisfy nilerr in forkseed sparse probe
- `34e9a6965c` fix(workspace): report a failed sparse checkout as WORKSPACE_SPARSE_FAILED on spawn
- `2c23f416c6` fix(sessions): expose sparsePaths and plan links on the session read model
- `613193660b` fix(sessions): a fork of a sparse session keeps its cone

**M3: scheduler, prompts, reports, QA loop**
- `8185108a21` feat(plans): dependency scheduler
- `46b7a0bbe8` feat(plans): milestone, QA, and defect prompts
- `6df7045130` feat(plans): advance milestones from worker reports
- `fb979e6530` feat(plans): QA verdicts and fix loop
- `c9876be1b6` feat(prompts): orchestrator plans tasks
- `e75cc0cb16` fix(plans): keep retries, QA rounds, and notices from stalling a plan
- `a59c599052` chore(plans): drop an unused report parameter
- `4618043a41` fix(plans): record the branch a milestone's work is on when it reports done

**M4: desktop app**
- `b626c618c5` feat(plans): plan summaries carry their milestone PRs
- `930d85c5a6` feat(app): plan queries
- `5c29f5d0dd` feat(app): plan with orchestrator from New Task
- `6a28a8e036` feat(app): tasks on the board
- `245f02e4f7` feat(plans): re-run QA after it passed
- `df23ee1f33` feat(app): task run view with milestone graph
- `4aff8b0408` fix(app): draw only a milestone's direct dependencies in the run graph
- `e31be47324` fix(app): fold a Task's agent sessions on project boards

**M5: docs, follow-ups, verification fixes**
- `afd8a38db5` feat(plans): block a milestone whose agent turns keep failing
- `e62533fdf7` fix(app): count only working agents in a Task's start line
- `8fa34741d8` fix(app): keep a Task card's status phrase whole on the board
- `82d2749536` docs(skills): plan worker, QA, and retry flows in using-ao
- `a1da4f289d` docs(plans): task plans guide and CLI docs
- `0643373da4` fix(plans): report a milestone's recorded branch, not its session's
- `e6a3603bdc` fix(app): title the run view rail with inspector sections
- `9ff81a721c` feat(plans): tell the orchestrator when a milestone blocks or runs out of attempts
- `4887c2ac59` fix(session): cache a session's plan env instead of reading the store per env
- `80fd9fc360` fix(plans): count only implementation milestones in a Task's summary
- `a7a6225a1c` fix(plans): say "using no branch" when a done milestone has none
- `c4511855de` fix(app): keep an event's verb visible in the run view
- `f7b046d2ce` docs(plans): branch handoff summary
- `851ee0da61` fix(plans): include the provider's message in a failing-turns block
- `cef1a96be3` docs(plans): correct limits and semantics after verification

**M6: end-to-end runs and tunings**
- `af68d0911c` fix(plans): clip report notes at a sentence boundary
- `773e764ef7` fix(prompts): QA falls back when the AO browser is unavailable
- `f4c044f60a` fix(prompts): orchestrator uses sparse checkouts for larger repos
- `7302b98621` fix(plans): a fresh orchestrator's first kickoff is not a deferral
- `4ed32c1978` fix(app): agent cards show the milestone's recorded work branch
- `a213dbca58` docs(plans): note the dev daemon's missing agent browser in the handoff
- `0abc397a1e` feat(plans): attach research workers to a Task with ao spawn --plan
- `e070a2306b` feat(plans): name the QA branch in the plan done notice
- `04adc9b260` fix(plans): pluralise the run view's active agent count

**Intake modes (direct / plan / auto), spec `docs/superpowers/specs/2026-11-14-task-intake-modes.md`**
- `43f1559821` fix(app): stop a done Task's run view reading as still running
- `9fb61713ba` docs(plans): add the Task intake modes spec
- `03f7aa629f` feat(plans): store Task intake modes and automation task modes
- `a2a9a3b265` feat(plans): let the orchestrator triage an auto Task
- `da21ed0557` feat(plans): add the triage API and ao plan triage
- `4ca2d76068` feat(automations): run an automation as a Task plan or an auto Task
- the desktop commit (New task Auto tab, run view triage/direct, board, Automations "Run as", locales) and this docs commit follow

## Migrations (all new; none on `main` modified)

| File | What it does |
|---|---|
| `20261114092547622_task_plans.sql` | `plans`, `plan_milestones`, `plan_milestone_deps`, `plan_events`; `sessions.plan_id`, `plan_milestone_id`, `sparse_paths`; CDC triggers writing `plan_updated` rows; widens the `change_log.event_type` CHECK in place (writable_schema, guarded). |
| `20261114111138604_task_plan_session_context.sql` | `sessions.plan_milestone_key`, `plan_prompt` (so restore keeps a worker's plan context). |
| `20261114112602243_task_plan_milestone_retry_base.sql` | `plan_milestones.retry_base` (fresh attempt budget on human retry) and `branch` (the work branch recorded at done). |
| `20261114161310228_task_plan_attached_sessions.sql` | Partial index `sessions_plan_idx` on `sessions(plan_id)` for listing a plan's research sessions. |
| `20261114231812030_task_intake_modes.sql` | Widens the `plans.state` CHECK in place with `triage` and `direct` (writable_schema, guarded); `plans.intake_mode`, `direct_session_id`, `triage_decision`, `triage_reason`, `triage_decided_by`, `triage_decided_at`, `triage_reminded_at`. Down maps triage/direct plans to cancelled. |
| `20261114231813267_automation_task_mode.sql` | `automations.task_mode` (`direct`/`plan`/`auto`, default direct); `automation_runs.plan_id` (FK to plans, ON DELETE SET NULL). |

## CLI

New `ao plan` (alias `plans`); `<plan-id>` may be omitted inside a plan session (`AO_PLAN_ID`):

```text
ao plan create --title <t> (--brief <text> | --brief-file <path>) [--ticket <ref>] [--max-parallel N] [--project <id>] [--json]
ao plan ls [--project <id>] [--json]
ao plan get [<plan-id>] [--json]
ao plan set-spec [<plan-id>] --file <plan.md>
ao plan milestone add [<plan-id>] --key <k> --title <t> (--description <text> | --description-file <path>) [--kind implement|qa] [--depends-on a,b] [--sparse <dir>]...
ao plan milestone update [<plan-id>] <key> [same flags]
ao plan milestone rm [<plan-id>] <key>
ao plan milestone retry [<plan-id>] <key>        # also re-runs QA: key qa
ao plan activate [<plan-id>]
ao plan cancel [<plan-id>] [--yes]
ao plan qa-result [<plan-id>] (--pass | --fail --milestone <key>) --note <text>   # QA session only
ao plan events [<plan-id>] [--after N] [--limit N] [--json]
```

Changed: `ao spawn --sparse <dir>` (repeatable), `ao spawn --plan <plan-id>`, `ao report --done --branch <name>`, `ao project set-config --sparse-enabled[=false] --sparse-include <dir>`.

`ao spawn --project <id> --plan <plan-id> --name "Research: <topic>" --output artifact --prompt "<question>"` attaches a research worker to a Task without a milestone. Unknown plan: 404 `PLAN_NOT_FOUND`; plan of another project (or no project): 400 `PLAN_PROJECT_MISMATCH`; `--kind orchestrator` or `--standalone`: usage error (the API answers 400 `PLAN_ATTACH_WORKER_ONLY` for an orchestrator). The orchestrator kills research workers with `ao session kill <id>` once their findings are in the plan; the service never does.

Worker env: `AO_PLAN_ID`, `AO_MILESTONE_KEY` (research workers get `AO_PLAN_ID` only).

## API routes (`/api/v1`)

| Route | Purpose |
|---|---|
| `POST /projects/{projectId}/plans` | Create a Task and send the orchestrator its plan request |
| `GET /projects/{projectId}/plans` | List a project's Tasks (summaries: phase, kanban column, display status, implement-milestone counts, live sessions, PRs) |
| `GET /plans/{planId}` | Plan, milestones (with recorded branch, session, PRs), `researchSessions` (sessions attached with `ao spawn --plan`: id, displayName, status, branch, ...), latest 100 events |
| `PATCH /plans/{planId}` | Title, `maxParallel`, `specMarkdown` (spec only while planning) |
| `POST /plans/{planId}/milestones` | Add a milestone (planning only) |
| `PATCH /plans/{planId}/milestones/{key}` | Edit a milestone (planning only) |
| `DELETE /plans/{planId}/milestones/{key}` | Remove a milestone (planning only) |
| `POST /plans/{planId}/milestones/{key}/retry` | Retry a failed milestone or blocked QA, or re-run a passed QA, with a fresh budget |
| `POST /plans/{planId}/activate` | Validate the graph, add QA, start scheduling |
| `POST /plans/{planId}/cancel` | Stop scheduling; sessions keep running |
| `POST /plans/{planId}/qa-result` | QA verdict (only the plan's current QA session) |
| `GET /plans/{planId}/events` | Activity log, oldest first (paged) |

Also: `POST /sessions` accepts `planId`; `POST /reports` accepts `branch`; session views carry `planId`, `planMilestoneId`, `sparsePaths`; SSE emits `plan_updated`.

## Config

Project config `sparseCheckout`:

```json
{ "sparseCheckout": { "enabled": true, "alwaysInclude": ["packages/shared"] } }
```

Off by default. Per Task: `maxParallel` (1 to 10, default 3). Limits: 20 milestones per Task including QA (19 implementation), 3 automatic attempts per milestone, 50 sparse paths, local single-repository projects only.

## Deferred minors (all milestones)

- Multi-step writes (add milestone with deps, activate, cancel) are not in one transaction.
- `ao plan milestone update --sparse ""` cannot clear sparse paths (sends `[""]`).
- `GET /projects/{unknown}/plans` returns `200 []` instead of 404; the list does one session read per milestone.
- The task-plans migration's Down path is untested.
- Cancel leaves failed milestones `failed` rather than `cancelled`.
- `CreatePlanRequest` has no attachments or `clientRequestId` yet (the New Task attach button is disabled in Plan mode).
- Sparse: an existing live worktree at the path is reused without applying the requested cone; a restore that re-creates a missing worktree is a full checkout; `rollbackSparseAdd` prunes repo-wide; forkseed may widen a parent of a cone entry; any `.git` stat error maps to `SPARSE_UNSUPPORTED`; a fork of a sparse session in a project that later disabled sparse is refused instead of falling back to full.
- QA: a no-milestone fail followed by a keyed fail drops the no-milestone failure from QA state (the orchestrator was told once); no timeout for a QA session that never answers the verdict request.
- Attempts: the cap counts defect respawns; an incomplete client-request replay consumes an attempt (untested).
- `addPlanEnv`'s one-time store read for sessions restored after a restart uses `context.Background` without a timeout.
- Recent-turn health check ignores conversation branch lineage (an edited-away failed turn could count once).
- Finished Task cards show avatars for every non-terminated (idle) session.
- The run view names agents by harness only (no model).
- Plans with no orchestrator session (one could not start) get no blocked/failed notices; the run view still shows them.
- The sidebar still lists every Task worker session under its project.
- The docs site (`frontend/src/docs`) was not built locally.

## Environment caveats (this machine)

- Full `npm run build` (electron-forge package) stops in `build-tmux`: the bundled tmux 3.5a segfaults on `new-session` during its terminfo check. The renderer bundle builds (`vite build --config vite.renderer.config.ts`).
- `go test -race ./...`: `adapters/agent/codex` `TestAuthStatusFindsNodeForNPMLauncherOutsidePATH` hits its 3 s deadline under `-race`; `adapters/runtime/tmux` `TestRuntimeIntegrationDeliversEnvironmentThroughAFile` leaves its fixed-name tmux session behind even when it passes, so the next run fails with "duplicate session" (`tmux kill-session -t TestRuntimeIntegrationDeliversEn-22d2f074`). Both pre-exist on `main`.
- `storage/sqlite` takes about 27 minutes under `-race` (over 10 minutes without), beyond `go test`'s default timeout; use `-timeout 40m`.
- Some pre-existing tmux tests build the runtime without `EnvDir` and can touch `~/.ao/data/runtime-env`.
- A dev daemon (`npm run build:daemon -- --dev`, or `go run`) lacks the agent-browser binary, so `ao browser` answers `AGENT_BROWSER_NOT_INSTALLED`; QA then falls back to its own headless browser or HTTP checks (as in the first M6 run). The packaged desktop app ships the binary.
- Codex plan workers need full-access permissions (their sandbox otherwise blocks the `ao report` loopback call); use claude-code workers otherwise.
- An upgraded or restarted daemon resumes every active Task on boot, including spawning milestones that became ready while it was down.

## How to run it

**Gates**

```bash
cd backend && go build ./... && go test ./...      # add -race -timeout 40m for the race pass
npm run lint                                       # go test + golangci-lint
npm run api && npm run sqlc && git status          # no diff
npm run frontend:typecheck
cd frontend && npx vitest run src/renderer/components/plan src/renderer/lib/plan-layout.test.ts \
  src/renderer/components/SessionsBoard.plans.test.tsx src/renderer/components/TaskComposer.plan.test.tsx \
  src/renderer/hooks/usePlans.test.tsx src/renderer/hooks/useWorkspaceQuery.plan.test.ts src/renderer/lib/event-transport.test.ts
cd frontend && npx vite build --config vite.renderer.config.ts --outDir /tmp/ao-renderer-check
```

**Desktop lab** (per `AGENTS.md`; never against `~/.ao`):

```bash
git worktree add --detach /tmp/ao-lab feat/task-plans
cd /tmp/ao-lab/frontend && npm ci && npm run build:daemon -- --dev
# strip the invoking agent's env, isolate data, use a scratch port (not 3047)
for v in $(env | cut -d= -f1 | grep -E '^(CLAUDE|CLAUDECODE)'); do unset $v; done
export AO_DATA_DIR=/tmp/ao-lab-data AO_RUN_FILE=/tmp/ao-lab-data/running.json AO_PORT=3049
export AO_ACP_RUNTIME_DIR=/tmp/ao-lab/frontend/resources/acp-runtime   # Chat mode
./node_modules/.bin/electron-forge start -- --remote-debugging-port=9333
```

Stop it by killing the `Electron.app/.../MacOS/Electron` main process (killing forge alone leaves the app running), then the daemon on the scratch port.

**End-to-end**

1. In the lab, add a local single-repository project whose worker agent is claude-code (or codex with full access) and whose orchestrator agent is configured.
2. New Task → **Plan**: title, brief (the ticket), optional ticket link → **Plan with orchestrator**. The run view opens; the orchestrator plans, registers milestones, and activates.
3. Without an orchestrator, drive it from the CLI with the same `AO_DATA_DIR`/`AO_RUN_FILE`/`AO_PORT`: `ao plan create …`, `ao plan set-spec <id> --file plan.md`, `ao plan milestone add <id> --key m1 …` (and `--depends-on`), `ao plan activate <id>`.
4. Watch: the board shows one Task card in Building; the run view's graph, agents, and events update live; `ao plan get <id>` and `ao plan events <id>` match.
5. Workers report done; dependents start with the recorded branches merged; QA merges everything, tests, and records its verdict. A fail routes back to the owning worker and QA re-runs.
6. On pass the card moves to Ready, the plan reads done, and the orchestrator receives `<ao-plan-done>`. Check the QA worktree (`$AO_DATA_DIR/worktrees/<project>/<qa-session>`) contains every milestone's commits.
7. Exercise Actions: Re-run QA, Show spec, Cancel plan (confirm dialog). Have a worker `ao report --stuck --note …` and confirm the orchestrator receives `<ao-milestone-blocked>` once.

## Orchestrator notices

`<ao-plan-done>` leads with `QA branch: <branch>`, the branch QA merged every milestone into and verified, resolved like a milestone's work branch (the QA worktree's checked-out branch, else the QA session's recorded branch marked `(unresolved: …)`, else `QA branch: (unresolved; ask the QA session …)`). The notice and the orchestrator prompt tell it to hand the human the QA branch as the combined result, with the milestone branches and PR links beneath it. `<ao-milestone-blocked>` and `<ao-milestone-failed>` are unchanged.

An auto Task's kickoff is `<ao-plan-triage id=…>` (the plan request's content plus the decision rule); the orchestrator answers with `ao plan triage <id> --plan|--direct --reason "…"`. `<ao-plan-triage-reminder>` arrives once, 15 minutes after the triage request landed, if no decision was made. `<ao-plan-triage-failed>` reports a direct worker that could not start (the Task stays in triage). A person's plan decision sends the ordinary `<ao-plan-request>`.

## Intake modes summary

- API: `POST /projects/{id}/plans` takes `mode: plan|auto`; `POST /plans/{id}/triage` `{decision, reason?, decidedBy?}` → `{plan}`; 409 `PLAN_NOT_IN_TRIAGE` after the first decision, 400 `INVALID_TRIAGE_DECISION|REASON|ACTOR`, `INVALID_PLAN_MODE`. Plan responses carry `intakeMode`, `directSessionId`, `triage`; phases add `triage` ("Deciding") and `direct` ("One worker", never a board card).
- Automations: `taskMode: direct|plan|auto` (400 `INVALID_AUTOMATION_TASK_MODE` for an unknown mode or a non-direct orchestrator automation); runs carry `planId`; plan-backed runs finish from the Task's phase, and a direct auto Task's run adopts the worker session.
- CLI: `ao plan create --mode`, `ao plan triage`, `ao automation create|update --task-mode`, a `PLAN` column in `ao automation runs`.
- Implementation rulings R1–R22 are recorded in the intake modes spec, §13.

## Harness model routes summary

Spec: `docs/superpowers/specs/2026-11-14-harness-model-routes.md`. Implementation rulings R1–R19 (plus M4's) are in §13. Commits: `3c7892e181` (spec), `e07df8c14b` (M1 foundations), `69b445b9ec` (M2 Claude Code), `6559038703` (M3 Codex and OpenCode), `4a50aaa4dd` (fix: route settings precedence), then the M4 app commit. No storage or migration change.

- API: `authentication.routes[]` (`AgentModelRoute`: `name?`, `kind`, `apiFamily`, `host?` as hostname[:port] only, `credential`, `credentialSource?`, `configSource?`, `verification`, `httpStatus?`, `isDefault?`), at most 16 per harness and sent with every verdict. New diagnostics have method `model_route` and the codes `route_identified|verified|rejected|unverifiable|inconclusive|access_challenge|unreachable|probe_disabled|credential_missing`. A configured verdict with routes gets a reason that names the route.
- Probe (`adapters/agent/modelroute`): one `GET` of the route's model list with exactly the harness's credential. 3 s timeout, no redirects, 1 MiB cap. Cached 5 minutes per URL and credential hash; the cache drops on Check again and on a runtime 401. It never sends a key over plain http off loopback, never probes a URL with userinfo, and makes no request for cloud routes. `AO_MODEL_ROUTE_PROBE=off` disables it and is read on every check.
- Claude Code: Bedrock, Vertex and Foundry from their `CLAUDE_CODE_USE_*` switch (environment or the user's settings.json) are recorded as unverifiable. An `ANTHROPIC_BASE_URL` gateway with no key is decided by its model list. A route rejection never blocks a launch.
- Codex: a custom `model_provider` (`base_url`, or built-in `oss`/`ollama`/`lmstudio`) is checked with the headers Codex sends. An unset `env_key` keeps the verdict unknown.
- OpenCode 1/2: providers with `options.baseURL` across the user, `OPENCODE_CONFIG*`, inline and managed layers are checked, at most 8 at a time. OpenCode 2 no longer counts `mcp_*` integrations as a model login.
- App: route line on the harness row; route status labels ("Configured, can't be verified", "Needs network access", "Can't reach provider"); a "Model route" row per route on the Health tab; a "Model route" group above Subscriptions for Claude Code and Codex, with the subscriptions-not-used note; route blocks in the copied health report; strings in all nine locales; a troubleshooting entry in the docs.

## Board lanes summary

Spec: `docs/superpowers/specs/2026-11-15-board-lanes-and-moves.md`; rulings R1–R39 are in its §12. Verification script: `docs/superpowers/plans/2026-11-15-board-lanes-verify.md`. User guide: `frontend/src/docs/content/guides/board.mdx`.

Commits: `86720ce798` (spec), `d63f6eae7b` (B1 lane model), `77a4dd908c` (B2 intents, moves, API, CLI), `5b92662c3a` (fix: merged/closed-only PRs refused before any review trigger), `ac4227566c` (B3 board UI, nine locales, DESIGN.md), `f183a332ec` (B4 docs), `684a10b9e4` (fix: un-accept on Building), `676280b51e`, `eacc5c3641`, `8dd345d603`, `4ba483f7ed` (B5: `hasPrProvider`, calm Needs you cards and legible queue text, Task status translations, keyboard menu access), `5c63f65182` (keyboard focus nits), `5be1e9dfdb` (scm parse fix).

- Lanes, left to right: In queue, Building, Validating, In review, Needs you, Ready (`boardKanbanColumnOrder` in `packages/product-ui/src/session-presentation.ts`); Archive stays the modal. Columns are still derived: `contract.DeriveKanbanPresentation` (sessions) and `domain.DerivePlanKanban` (Tasks) return a column and a `Stage`. Stage is the lifecycle lane whenever the column was decided by a board request or by Needs you; otherwise it equals the column. The read models carry it as `kanbanStage`.
- Session rules, first match wins: terminated → archive; `accepted` with no PR → Ready "Accepted"; provisioning → In queue "Starting"; then the lifecycle placement. After that, an `attention` request → Needs you "Paused for you"; otherwise Needs you when any of these hold:
  - the worker is blocked or asked a question;
  - the stage is In review with CI failing, merge conflicts or changes requested;
  - an idle or exited worker's newest report is `needs_input` or `stuck`;
  - a finished worker has commits, no PR, no artifacts, no PR provider, is not plan-managed and was not interrupted ("Awaiting review").
  Exited alone, No signal, Commented, and Awaiting review with a PR stay where they were.
- Task rules: cancelled/direct → archive; `hold` → In queue "On hold"; `attention` → Needs you "Paused for you"; triage → In queue "Deciding" for 15 minutes from creation, then Needs you "Needs a decision"; failed → Needs you; any blocked milestone → Needs you "Blocked"; otherwise the phase lanes as before.
- Automations: an enabled automation of the project is an In queue card ("Scheduled", next run as a short date and time), built client-side from `useAutomations()`.
- Migration `20261115105225340_board_requests` (in `shippedMigrations`): `sessions.board_request` ('' | attention | accepted) and `plans.board_request` ('' | hold | attention), each with `board_request_at`. Queries `SetSessionBoardRequest`, `SetPlanBoardRequest`, `ListLatestReportStates`, `CountOpenAutomationRuns`.
- API:
  - `POST /sessions/{id}/board-move` and `POST /plans/{id}/board-move` take `{to}` and answer with `SessionResponse` and `PlanDetailResponse` respectively. Errors are 400 `INVALID_BOARD_COLUMN`, or 409 `BOARD_MOVE_UNSUPPORTED` with `details.reason`: one of the twelve refusals, or `review_trigger_failed`. Also 409 `SESSION_TERMINATED` and 409 `PLAN_NOT_LIVE`.
  - `POST /automations/{id}/run-now` answers 202 with the run summary, or 409 `AUTOMATION_DISABLED` or `AUTOMATION_RUN_ACTIVE` (the latter also when a run is already pending).
  - `SessionResponse` gains `kanbanStage` and `hasPrProvider`; plan summaries gain `kanbanStage`, and their live sessions `hasPrProvider`.
- CLI: `ao session move <id> --to <lane>`, `ao plan move [<plan-id>] --to <lane>`, `ao automation run-now <id>`. `ao session ls` and `ao plan ls` have a LANE column showing e.g. `needs_you (in review)`.
- Agent notices (`session.Service.Send`): `<ao-board-hold>`, `<ao-board-resume>` (names the lane), `<ao-board-deliver>` (push, open a PR against the base branch, request a review, or `ao report --stuck`); to the orchestrator, `<ao-plan-hold>` (with the summarise ask for Needs you) and `<ao-plan-resume>`. A held Task spawns no milestone workers, runs no deferred direct triage spawn, retries no kickoff and sends no triage reminder; release runs what was deferred. Board moves on a Task are plan events of kind `board_move`, and a held direct decision is `triage_deferred`.
- App: drag (dnd-kit in `frontend/src/renderer/components/BoardMoves.tsx`, since product-ui's import boundary forbids it) and "Move to" in the card menu. Lanes that would refuse the dragged card dim and say why (prediction in `lib/board-moves.ts`, server refusal as a translated toast). Shift+F10 and the menu key open a card's menu; focus returns to the card; drag announcements are localized. Needs you cards print `stage · reason` with no pulse or tint. Moves are offered only on this machine's own boards, not on shared or cloud projects.
- Rulings of note:
  - R2 and R9: exited alone is not Needs you.
  - R13: run-now refused while a run is pending.
  - R17: drafts are refused `pr_is_draft`, and a move never writes to the provider.
  - R18: refusals before side effects.
  - R20: Validating refused for merged/closed-only PRs.
  - R22 and R23: hold semantics.
  - R27: stage for board requests.
  - R28: a harness may block the deliver push, and the card then reads Stuck.
  - R29–R32: renderer drag, menu, time format, local boards only.
  - R33: un-accept from Building.
  - R34–R38: ao-verify's B4 findings.
  - R39: the scm parse.
- SCM parse fix (R39, pre-existing on `main`): `parseGitHubRepo` read any remote with no `://` and one slash as GitHub `owner/repo` shorthand, so a local bare origin like `/tmp/ao-e2e-todo3.git` became `github.com/tmp/ao-e2e-todo3`. That made `hasPrProvider` true, which kept finished no-provider workers out of Needs you. It also made the SCM observer poll api.github.com for that path every 30 s with the user's token. The shorthand now rejects paths on disk; GitLab has no shorthand branch.
- Deferred:
  - The mobile app keeps its own lane mapping and treats the new lanes through its status fallback (R26).
  - The sidebar's needs-you set (`sessionNeedsYou`) is not aligned with the lane (R15).
  - The `accepted` request is cleared in `ReconcileSessionOutputType` only once a PR exists; until then the reducer already ignores it (R10).
  - A stale tmux session `TestRuntimeIntegrationDeliversEn-22d2f074` (Nov 14) makes the tmux integration test fail on this machine.
  - The codex `TestAuthStatusFindsNodeForNPMLauncherOutsidePATH` times out under a full parallel run but passes alone.
- Evidence (ao-verify): `/tmp/ao-plans/m10/ev/b2`, `/tmp/ao-plans/m10/ev/b4` (REPORT-B4.md and screenshots), `/tmp/ao-plans/m10/ev/b5`. Lab runbook `/tmp/ao-plans/m10/README.md`.
