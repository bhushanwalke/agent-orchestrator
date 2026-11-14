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

## Migrations (all new; none on `main` modified)

| File | What it does |
|---|---|
| `20261114092547622_task_plans.sql` | `plans`, `plan_milestones`, `plan_milestone_deps`, `plan_events`; `sessions.plan_id`, `plan_milestone_id`, `sparse_paths`; CDC triggers writing `plan_updated` rows; widens the `change_log.event_type` CHECK in place (writable_schema, guarded). |
| `20261114111138604_task_plan_session_context.sql` | `sessions.plan_milestone_key`, `plan_prompt` (so restore keeps a worker's plan context). |
| `20261114112602243_task_plan_milestone_retry_base.sql` | `plan_milestones.retry_base` (fresh attempt budget on human retry) and `branch` (the work branch recorded at done). |

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

Changed: `ao spawn --sparse <dir>` (repeatable), `ao report --done --branch <name>`, `ao project set-config --sparse-enabled[=false] --sparse-include <dir>`.

Worker env: `AO_PLAN_ID`, `AO_MILESTONE_KEY`.

## API routes (`/api/v1`)

| Route | Purpose |
|---|---|
| `POST /projects/{projectId}/plans` | Create a Task and send the orchestrator its plan request |
| `GET /projects/{projectId}/plans` | List a project's Tasks (summaries: phase, kanban column, display status, implement-milestone counts, live sessions, PRs) |
| `GET /plans/{planId}` | Plan, milestones (with recorded branch, session, PRs), latest 100 events |
| `PATCH /plans/{planId}` | Title, `maxParallel`, `specMarkdown` (spec only while planning) |
| `POST /plans/{planId}/milestones` | Add a milestone (planning only) |
| `PATCH /plans/{planId}/milestones/{key}` | Edit a milestone (planning only) |
| `DELETE /plans/{planId}/milestones/{key}` | Remove a milestone (planning only) |
| `POST /plans/{planId}/milestones/{key}/retry` | Retry a failed milestone or blocked QA, or re-run a passed QA, with a fresh budget |
| `POST /plans/{planId}/activate` | Validate the graph, add QA, start scheduling |
| `POST /plans/{planId}/cancel` | Stop scheduling; sessions keep running |
| `POST /plans/{planId}/qa-result` | QA verdict (only the plan's current QA session) |
| `GET /plans/{planId}/events` | Activity log, oldest first (paged) |

Also: `POST /reports` accepts `branch`; session views carry `planId`, `planMilestoneId`, `sparsePaths`; SSE emits `plan_updated`.

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
