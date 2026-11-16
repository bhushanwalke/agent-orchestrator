# Task Plans Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Tasks (plans) to AO: a ticket created in the app is planned by the orchestrator, implemented by parallel milestone workers in sparse worktrees in dependency order, verified by a QA worker with a fix loop, and shown on the board and in a run view.

**Architecture:** New `plans`/`plan_milestones`/`plan_milestone_deps`/`plan_events` tables and a `service/plan` package (CRUD, graph validation, a tick-driven scheduler woken by worker reports) behind new `/api/v1/plans` routes and an `ao plan` CLI. Spawns carry a `PlanContext` and optional `SparsePaths` that the git worktree adapter turns into a cone-mode sparse checkout. The desktop gets a Plan mode in New Task, plan cards on the board, and a run view with an SVG dependency graph, agent cards, and events.

**Tech Stack:** Go 1.2x (chi, sqlc, goose, Cobra), SQLite, React 19 + TanStack Router/Query + Tailwind + shadcn, vitest.

**Spec:** `docs/superpowers/specs/2026-11-14-task-plans-design.md` (read it first; section numbers below refer to it).

## Global Constraints

- Work only on branch `orchestrator-plus` in a worktree; never commit to `main`. Conventional commits, one commit per task.
- Follow `AGENTS.md`: CLI is a thin HTTP client; no derived status persisted; new migration via `npm run migration:create -- task_plans` and append to `shippedMigrations`; never hand-edit `storage/sqlite/gen`; edit `dto.go` + `specgen/build.go` then `npm run api` and commit `openapi.yaml` + `frontend/src/api/schema.ts`.
- All copy sentence case; follow `DESIGN.md` (no glows, gradients, metric tiles; reuse `SessionCardView`, `InspectorSection`, shadcn primitives).
- Max milestones per plan 20; max parallel 1–10 default 3; milestone key regex `^[a-z0-9][a-z0-9-]{0,31}$`; `qa` key reserved; retry limit 3 attempts.
- Loopback daemon stays unauthenticated on `127.0.0.1`; no new network binds.
- Gates per milestone: `cd backend && go build ./... && go test ./...`; `npm run lint`; `npm run api` leaves no diff; `npm run frontend:typecheck`; vitest for touched frontend files.

## Review Focus

1. A done report from a session that belongs to no milestone (ordinary worker) must be a no-op in `OnReport`, never an error that breaks `ao report`. Test in M3 Task 3.
2. Two milestones with the same single dependency both stack on its branch; the second must not fail because the branch is "taken". `BaseBranch` is the dep's branch, `Branch` stays the default generated name. Test in M3 Task 2.
3. `ao plan qa-result --fail --milestone m1` while m1's session is mid-turn: delivery goes through the guarded path; if refused (busy/needs-input), the milestone stays `ready` and the scheduler retries delivery next tick instead of spawning a duplicate worker. Test in M3 Task 4.
4. Sparse spawn on a project with `sparseCheckout.enabled=false` falls back to a full checkout for plans (event `sparse_disabled`) but is a 400 for `ao spawn --sparse`. Test in M2 Task 3.
5. Cancelling a plan while the scheduler tick is mid-spawn: the tick re-reads plan state before each spawn and skips cancelled plans; the spawned session (if any) is recorded with its milestone marked `cancelled`. Test in M3 Task 5.

---

## Milestone M1 — Plan data model, service core, API, CLI

Deliverable: `ao plan create/ls/get/set-spec/milestone add|update|rm/activate/cancel/events` work against a dev daemon; activation validates the graph; nothing spawns yet (scheduler stub). Verifiable with curl and the CLI.

### Task 1.1: Migration and store

**Files:**
- Create: `backend/internal/storage/sqlite/migrations/<id>_task_plans.sql` (spec §3, incl. CDC triggers)
- Create: `backend/internal/storage/sqlite/queries/plans.sql`
- Create: `backend/internal/storage/sqlite/store/plan_store.go`, `plan_store_test.go`
- Modify: `backend/internal/storage/sqlite/migrate_burned_versions_test.go` (append id), `backend/internal/cdc/event.go` (`EventPlanUpdated`)
- Modify: `backend/internal/storage/sqlite/queries/sessions.sql` + `store/session_store.go` to read/write `plan_id`, `plan_milestone_id`, `sparse_paths`

**Interfaces (Produces):**
```go
InsertPlan(ctx, domain.Plan) error; GetPlan(ctx, PlanID) (domain.Plan, bool, error)
ListPlans(ctx, ProjectID) ([]domain.Plan, error); ListActivePlans(ctx) ([]domain.Plan, error)
UpdatePlan(ctx, domain.Plan) error
InsertMilestone(ctx, domain.Milestone) error; UpdateMilestone(ctx, domain.Milestone) error
DeleteMilestone(ctx, MilestoneID) error; ListMilestones(ctx, PlanID) ([]domain.Milestone, error)  // DependsOn keys filled
GetMilestoneBySession(ctx, SessionID) (domain.Milestone, bool, error)
SetMilestoneDeps(ctx, MilestoneID, []MilestoneID) error
InsertPlanEvent(ctx, domain.PlanEvent) error; ListPlanEvents(ctx, PlanID, afterID int64, limit int) ([]domain.PlanEvent, error)
```
Session store: `SessionRecord.PlanID`, `.PlanMilestoneID`, `.Metadata.SparsePaths []string` persisted.

- [ ] Write `plan_store_test.go` (round trip of plan + milestones + deps, cascade on plan delete, `GetMilestoneBySession`, a change_log row with `event_type='plan_updated'` after `UpdateMilestone`).
- [ ] Run `go test ./internal/storage/sqlite/store/ -run Plan` → FAIL (no such table / methods).
- [ ] Add migration, queries, `npm run sqlc`, implement store methods.
- [ ] Run the tests → PASS. Run `go test ./internal/storage/sqlite/...` → PASS.
- [ ] Commit `feat(plans): add plan tables, store, and CDC events`.

### Task 1.2: Domain types and validation

**Files:** Create `backend/internal/domain/plan.go`, `plan_test.go`. Modify `backend/pkg/contract/kanban.go` (new `DisplayStatus` constants from spec §4).

**Produces:** types in spec §4; `ValidateMilestoneKey(string) error`; `(Milestone) Validate() error`; `ValidatePlanGraph([]Milestone) error`; `DerivePlanPhase(Plan, []Milestone) PlanPhase`; `DerivePlanKanban(PlanPhase, []Milestone) (contract.KanbanColumn, contract.DisplayStatus)`; `TopoOrder([]Milestone) ([]Milestone, error)`.

- [ ] Write table tests: key regex accept/reject; cycle m1→m2→m1 rejected with message naming both keys; dep on unknown key; qa depended on; phase table from spec §4 (one row per condition, including `Implementing 2/5`).
- [ ] Run → FAIL. Implement. Run → PASS. Commit `feat(plans): plan domain types, graph validation, phase derivation`.

### Task 1.3: Plan service (no scheduler yet)

**Files:** Create `backend/internal/service/plan/{service.go,store.go,types.go,service_test.go}`.

**Consumes:** store from 1.1, domain from 1.2. **Produces:** `plan.Service` with `New(Deps)`, `Create`, `Get(ctx, PlanID) (Detail, error)` where `Detail{Plan, Milestones []Milestone, Events []PlanEvent, Phase, Kanban, DisplayStatus}`, `List(ctx, ProjectID) ([]Summary, error)`, `Update`, `SetSpec`, `AddMilestone`, `UpdateMilestone`, `RemoveMilestone`, `Activate`, `Cancel`, `RetryMilestone`, `Events`. `Deps.Sessions` interface per spec §5 (fake in tests). `Activate` calls `s.scheduler.Tick` if non-nil (nil in M1).

- [ ] Tests: Create inserts `state=planning`, ensures orchestrator, stores its id, sends kickoff text containing `<ao-plan-request id="plan_…">` and the brief; `Send` refusal → event `kickoff_deferred`, no error; AddMilestone after Activate → `PLAN_NOT_EDITABLE`; Activate without implement milestone → `PLAN_GRAPH_INVALID`; Activate auto-adds `qa`; Cancel marks non-terminal milestones cancelled; project kind Scratch → `PLAN_PROJECT_KIND_UNSUPPORTED`.
- [ ] Run → FAIL. Implement. Run → PASS. Commit `feat(plans): plan service with planning lifecycle`.

### Task 1.4: HTTP routes, DTOs, OpenAPI

**Files:** Create `backend/internal/httpd/controllers/plans.go`, `plans_test.go`. Modify `controllers/dto.go` (spec §8 DTOs), `apispec/specgen/build.go` (`planOperations()` + `schemaNames`), router registration beside cues, `daemon/daemon.go` wiring (`plansvc.New`).

- [ ] Tests with httptest and a fake service for each route in spec §8: 201/200/204 shapes, 400 `INVALID_MILESTONE` with `details.field`, 404 `PLAN_NOT_FOUND`, 409 `PLAN_NOT_EDITABLE`, path unescape of ids.
- [ ] Run → FAIL. Implement. `npm run api`. Run `go test ./internal/httpd/...` → PASS (spec drift + parity). Commit `feat(plans): plan HTTP API and OpenAPI`.

### Task 1.5: CLI `ao plan`

**Files:** Create `backend/internal/cli/plan.go`, `plan_test.go`. Modify `cli/root.go` to register. Create `backend/internal/skillassets/using-ao/commands/plan.md`; add a row to `SKILL.md`.

- [ ] Table tests (pattern: `cue_test.go`): every subcommand's request body against a fake daemon; missing `--title` exits 2; daemon envelope error exits 1 with message and request id; `AO_PLAN_ID` fallback; `--brief-file`/`--description-file` read; `ao plan get` prints a milestones table with columns `KEY KIND STATUS DEPS SESSION`.
- [ ] Run → FAIL. Implement. Run → PASS. Commit `feat(cli): ao plan commands`.

### M1 verification (ao-verify)

```bash
cd backend && go build -o ./bin/ao ./cmd/ao
mkdir -p /tmp/ao-plans && AO_DATA_DIR=/tmp/ao-plans/data AO_RUN_FILE=/tmp/ao-plans/running.json AO_PORT=3047 ./bin/ao daemon &   # keep for later milestones
export AO_DATA_DIR=/tmp/ao-plans/data AO_RUN_FILE=/tmp/ao-plans/running.json AO_PORT=3047
./bin/ao project add --path <a git repo with a remote default branch>
./bin/ao plan create --project <id> --title "Demo" --brief "Add X" ; ./bin/ao plan ls --project <id>
./bin/ao plan milestone add <plan> --key m1 --title A --description "do A"
./bin/ao plan milestone add <plan> --key m2 --title B --description "do B" --depends-on m1
./bin/ao plan milestone add <plan> --key m3 --title C --description "do C" --depends-on m2 ; ./bin/ao plan milestone update <plan> m1 --depends-on m3   # expect PLAN_GRAPH_INVALID at activate
./bin/ao plan activate <plan>   # fix the cycle first, then expect state=active, qa auto-added
curl -s localhost:3047/api/v1/plans/<plan> | jq .plan.phase   # "implementing"
```
Expected: orchestrator session created (`ao orchestrator ls`), kickoff message visible in its conversation (or `kickoff_deferred` event if the harness is not installed), all gates green.

---

## Milestone M2 — Cone-mode sparse worktrees

Deliverable: `ao spawn --sparse <dir>` and project `sparseCheckout` config create a cone-mode sparse worktree; the worker prompt explains it.

### Task 2.1: Project config and spawn plumbing

**Files:** Modify `domain/projectconfig.go` (+tests) for `SparseCheckoutConfig` (spec §7.1); `ports/session.go` (`SpawnConfig.SparsePaths`, `PlanContext` type now so M3 can use it); `ports/outbound.go` (`WorkspaceConfig.SparsePaths`); `httpd/controllers/dto.go` (`SpawnSessionRequest.SparsePaths []string`, `ProjectConfig` DTO if mirrored); `cli/spawn.go` (`--sparse`, usage error with `--standalone`); `session_manager/manager.go` Spawn: copy `SparsePaths` into `WorkspaceConfig`, persist on the session, refuse when project `sparseCheckout.enabled` is false with `SPARSE_CHECKOUT_DISABLED`, refuse for Scratch/workspace kinds with `SPARSE_UNSUPPORTED`.

- [ ] Tests: config validation (repo-relative, max 50); CLI flag → request body; manager refusals (fake workspace adapter records the config it received).
- [ ] Run → FAIL. Implement. Run → PASS. `npm run api`. Commit `feat(workspace): sparse checkout config and spawn plumbing`.

### Task 2.2: Worktree adapter

**Files:** Modify `adapters/workspace/gitworktree/commands.go` (`worktreeAddNoCheckoutArgs`, `sparseInitArgs`, `sparseSetArgs`, `readTreeArgs`), `workspace.go` `Create` (spec §7.2), `workspace_integration_test.go`; `forkseed` test for the cone widening.

- [ ] Integration test (uses the package's existing temp-repo helpers): repo with `a/`, `b/`, `c/` dirs and a root `README`; create with `SparsePaths: ["a","c"]`; assert `git sparse-checkout list` == `a\nc`, `b/` absent, `README` present, `git -C wt config core.sparseCheckoutCone` true; a bogus path makes Create fail with `ErrSparseCheckoutFailed` and leaves no worktree registered.
- [ ] Run → FAIL. Implement. Run → PASS. Commit `feat(workspace): cone-mode sparse worktrees`.

### Task 2.3: Prompt section and plan fallback

**Files:** Modify `session_manager/prompt.go` (`sparseCheckoutPrompt(paths []string)`), `prompt_test.go`; the plan scheduler fallback is tested in M3 but the event kind `sparse_disabled` and the helper `effectiveSparsePaths(project, milestone) []string` live in `service/plan/scheduler.go` (stub file now).

- [ ] Test: a worker spawned with sparse paths has the §7.3 section with the paths listed; without, no section.
- [ ] Run → FAIL. Implement. Run → PASS. Commit `feat(prompts): explain sparse checkouts to workers`.

### M2 verification (ao-verify)

With the M1 daemon: enable sparse in project config (`ao project set-config <id> --config-json` preserving fields, or PUT `/projects/{id}/config` with `{"sparseCheckout":{"enabled":true,"alwaysInclude":["scripts"]}}`), then
`./bin/ao spawn --project <id> --name sparse-test --mode tui --harness claude-code --sparse backend/internal/cli --prompt "run git sparse-checkout list and ls, then stop"`.
Expected: worktree under `~/.ao`-equivalent data dir has only `backend/internal/cli` + `scripts` + root files; `git sparse-checkout list` matches; session prompt (via `ao session history`) contains "Sparse checkout". Disabled config → 400 `SPARSE_CHECKOUT_DISABLED`. Gates green.

---

## Milestone M3 — Scheduler, report hooks, QA loop, orchestrator planning prompt

Deliverable: an activated plan spawns workers in parallel/dependency order, promotes on done reports, spawns QA, routes QA failures back, finishes on pass, and the orchestrator knows the planning protocol. Verifiable with the `fake` harness and by driving `ao report`/`ao plan qa-result` by hand.

### Task 3.1: Scheduler core

**Files:** `service/plan/scheduler.go` (`Scheduler` with `Tick(ctx, now) error`, `Wake()`, `Start(ctx) <-chan struct{}`), `scheduler_test.go`; `observe/plans/observer.go` (copy of `observe/automations`, 15 s tick); `daemon` wiring (start observer; `Service.scheduler` set).

**Produces:** spawn requests built per spec §6 table (`buildMilestoneSpawn(plan, ms, deps, project) ports.SpawnConfig`), `ClientRequestID = "plan:"+msID+":"+attempt`.

- [ ] Tests with fakes: (a) m1,m2 independent, m3 depends on both, cap 3 → tick spawns m1,m2 only; m1 done → nothing; m2 done → m3 spawns with prompt listing both branches; (b) cap 2 with 3 ready → 2 spawns, third on next tick after one done; (c) single-dep milestone uses dep branch as `BaseBranch` and generated `Branch`; (d) all implement done → QA spawn with full checkout, display name `{title} · QA`; (e) running session terminated → `failed`, re-spawn with attempt 2, stops at 3; (f) spawn error → failed + retry; (g) cancelled plan skipped mid-tick (Review Focus 5); (h) sparse disabled project → full checkout + one `sparse_disabled` event.
- [ ] Run → FAIL. Implement. Run → PASS. Commit `feat(plans): dependency scheduler`.

### Task 3.2: Milestone and QA prompts

**Files:** `service/plan/prompts.go` (+test) producing spec §6.1/§6.2/§6.3 text; `session_manager/manager.go` appends `PlanContext.PromptSection` to the worker system prompt and exports `AO_PLAN_ID`/`AO_MILESTONE_KEY`; persist `plan_id`/`plan_milestone_id`.

- [ ] Tests: golden-ish assertions on required lines (merge command per dep, `ao plan qa-result` lines in QA prompt, spec clipped at 8000 chars); env vars present in the launch env.
- [ ] Run → FAIL. Implement. Run → PASS. Commit `feat(plans): milestone, QA, and defect prompts`.

### Task 3.3: Report hook

**Files:** `service/plan/service.go` `OnReport(ctx, domain.ReportRecord)`; `service/report/service.go` gains a second callback option `WithPlanObserver(func(context.Context, domain.ReportRecord))` (do not change existing `onCreated`); daemon wiring.

- [ ] Tests: done → milestone done + Wake; stuck → blocked, checkpoint → running again; report from a non-plan session → no-op (Review Focus 1); QA done without verdict → failed + message to QA session.
- [ ] Run → FAIL. Implement. Run → PASS. Commit `feat(plans): advance milestones from worker reports`.

### Task 3.4: QA results and fix loop

**Files:** `service/plan/service.go` `RecordQAResult`; controller + CLI already route it (M1) — add the authorization check and tests; scheduler handles `ready` milestones with `qa_verdict=fail` (reuse live session via guarded `Send` with the §6.3 message; spawn on same branch when terminated).

- [ ] Tests: pass → plan done + `<ao-plan-done>` sent to orchestrator; fail m1 with live session → Send called, no spawn, status running; fail with terminated session → spawn with `Branch` = old branch; Send refused (busy) → stays ready, retried next tick, no duplicate (Review Focus 3); wrong session → `PLAN_QA_SESSION_REQUIRED`; QA re-promotes only after all failed milestones done.
- [ ] Run → FAIL. Implement. Run → PASS. Commit `feat(plans): QA verdicts and fix loop`.

### Task 3.5: Orchestrator planning prompt and kickoff retry

Note from M1: `Create` already retries a deferred kickoff in the background for 5 minutes (`Service.RetryDeferredKickoff`). The scheduler tick MUST also sweep every `planning` plan without a `kickoff_sent` event and call that method, so a daemon restart or an expired window cannot strand a plan.

**Files:** `session_manager/prompt.go` `orchestratorPlanPrompt()` (spec §6.4) appended in `orchestratorSystemPrompt`; `prompt_test.go`; scheduler retries `kickoff_deferred` plans each tick.

- [ ] Tests: orchestrator prompt contains the six numbered steps and the exact CLI forms; deferred kickoff delivered on a later tick once `Send` succeeds.
- [ ] Run → FAIL. Implement. Run → PASS. Commit `feat(prompts): orchestrator plans tasks`.

### M3 verification (ao-verify)

Use the `fake` harness if the project config allows it (`worker.agent: fake`), else `claude-code` in TUI mode. Create a plan with m1, m2 (independent), m3 (depends on m1,m2), activate, and observe:
1. Two worker sessions appear immediately (`ao session ls --project`), m3 pending with `Waiting for m1, m2` in `ao plan get`.
2. From inside each worker's env (or with `AO_SESSION_ID=<id>` exported in a shell) run `./bin/ao report --done --note ok` for m1 and m2 → within 15 s m3 spawns.
3. m3 done → QA session spawns with full checkout; `AO_SESSION_ID=<qa> ./bin/ao plan qa-result <plan> --fail --milestone m2 --note "broken"` → m2's session receives the `<ao-qa-defect>` message (check its conversation/terminal) and `ao plan get` shows phase `fixing`.
4. m2 `--done` again → new QA session; `--pass` → phase `done`, orchestrator conversation has `<ao-plan-done>`.
5. Kill a running worker's session (`ao session kill`) → milestone fails and re-spawns, attempt increments; third kill leaves it failed and phase `failed`.
Gates green.

---

## Milestone M4 — Desktop app

Deliverable: Plan mode in New Task, plan cards on the board, run view with graph, agent cards, and events; agent cards open the session view.

### Task 4.1: API hooks and types

Note from M1: `plan_updated` must be added to the frontend event-transport listener list (it is not there yet).

**Files:** Create `frontend/src/renderer/hooks/usePlans.ts` (`usePlansQuery(projectId)`, `usePlanQuery(planId)`, `usePlanMutations()` for cancel/retry/patch), `frontend/src/renderer/types/plan.ts` (mapped from `components['schemas']`), SSE invalidation in the existing events hook for `plan_updated`.

- [ ] Tests: query keys, invalidation on `plan_updated` and `session_updated` events (follow an existing hook test pattern).
- [ ] Implement. `npm run typecheck` → PASS. Commit `feat(app): plan queries`.

### Task 4.2: Plan mode in New Task

**Files:** Modify `components/TaskComposer.tsx` (+`TaskComposer.plan.test.tsx`), `NewTaskDialog.tsx`, `NewChatPage.tsx` as needed; i18n `newTask.plan.*`.

- [ ] Test: switching to Plan hides agent/model pickers, shows Title and Ticket link, submit posts `{title, brief, ticketRef}` to `/api/v1/projects/{id}/plans` and navigates to `/projects/$projectId/plans/$planId`; last mode persisted per project.
- [ ] Run → FAIL. Implement (segmented control from existing shadcn `ToggleGroup`/tabs primitive in `components/ui`). Run → PASS. Commit `feat(app): plan with orchestrator from New Task`.

### Task 4.3: Board plan cards

**Files:** Modify `components/SessionsBoard.tsx`, `SessionsBoardAdapters.tsx` (`BoardPlanCardAdapter`, `toBoardPlanPresentation`), `stores/ui-store.ts` (`showPlanSessions`), tests `SessionsBoard.plans.test.tsx`; i18n `board.plans.*`.

- [ ] Test: plan in `building` renders in the Working lane with "3/5 milestones" and avatar strip; sessions with `planId` hidden until toggle; click navigates to run route; context menu items present.
- [ ] Run → FAIL. Implement. Run → PASS. Commit `feat(app): tasks on the board`.

### Task 4.4: Run view

**Files:** Create `routes/_shell.projects.$projectId_.plans.$planId.tsx`, `components/plan/PlanRunView.tsx`, `components/plan/PlanGraph.tsx`, `components/plan/PlanAgentsRail.tsx`, `components/plan/PlanEvents.tsx`, `lib/plan-layout.ts` (pure layering function `layoutPlan(milestones) → {nodes:[{key,x,y,layer}], edges:[{from,to}]}`), tests `plan-layout.test.ts`, `PlanGraph.test.tsx`, `PlanRunView.test.tsx`; i18n `plan.*`. Route tree regenerates via the existing TanStack plugin on build/typecheck.

- [ ] Tests: `layoutPlan` places m3 in layer 2 when it depends on m1 (layer 0) and m2 (layer 0), QA last; glyph per status; arrow keys move selection; agent card click navigates to `/projects/$projectId/sessions/$sessionId`; events list newest first; Actions menu has Cancel plan with confirm.
- [ ] Run → FAIL. Implement per spec §11.3 (SVG only). Run → PASS. `npm run typecheck`. Commit `feat(app): task run view with milestone graph`.

### M4 verification (ao-verify)

Desktop lab per `AGENTS.md` ("Desktop lab") against the M1 daemon data dir, or `npm run dev:web` with `ao preview` if the lab is unavailable. Check: New Task → Plan → creates plan and lands on the run view; board shows the Task card in Working with avatars; run view graph matches `ao plan get`; clicking an agent card opens the session view; events update live when `ao report --done` is run; Cancel plan asks for confirmation and moves the card to the archive state. Report screenshots and `DESIGN.md` deviations.

---

## Milestone M5 — Docs, skill assets, release hygiene

- [ ] Task 5.1: `frontend/src/docs/content/guides/task-plans.mdx` (user guide: create a Task, what the orchestrator does, milestones, sparse checkout, QA loop, limits) and link it from `guides/meta.json` and `configuration/projects.mdx` (new `sparseCheckout` row). `docs/STATUS.md` shipped bullet. `docs/cli/README.md` plan commands.
- [ ] Task 5.2: `using-ao` skill: `commands/plan.md` complete, `spawn.md` `--sparse`, `report.md` note about plan milestones.
- [ ] Task 5.3: Full gate run: `npm run lint`, `go test -race ./...`, `npm run api` no diff, `npm run frontend:typecheck`, `cd frontend && npm run build`, `npm run product-ui:check` if `packages/product-ui` changed. Fix anything red. Commit `docs(plans): task plans guide and CLI docs`.

### M5 verification (ao-verify)
Re-run every gate from a clean `git status`; confirm `git diff --stat main..orchestrator-plus` touches no migration already on main and no `gen/` edits by hand (`npm run sqlc` no diff).

---

## Milestone M6 — End-to-end: TODO app

Deliverable: a real run with real agents on a throwaway repo.

- [ ] Task 6.1: Create `/tmp/ao-e2e-todo` as a git repo with an `origin` remote (a local bare repo at `/tmp/ao-e2e-todo.git` is enough for branches; PRs are skipped without GitHub) containing a minimal Node project: `package.json` with `"test": "node --test"`, an empty `src/`, a README stating "TODO web app: Express API + static frontend + tests". Register it: `ao project add --path /tmp/ao-e2e-todo`, set `worker.agent` and `orchestrator.agent` to the harnesses installed on this machine (prefer `codex` with `astra`/`sol` if available, else `claude-code`), `sparseCheckout.enabled=true`, `workersRequestReview=false`.
- [ ] Task 6.2: In the app (or `ao plan create`), create the Task: title "TODO app MVP", brief: "Build a TODO web app: (1) Express REST API with in-memory store: GET/POST/PATCH/DELETE /todos; (2) static frontend (vanilla JS) listing, adding, toggling, deleting todos against the API; (3) unit tests for the API and an integration test that boots the server and exercises the frontend flow with fetch. Use node --test. Document how to run it in README." Expect the orchestrator to spawn 2–3 research workers (API design, frontend approach, test strategy), then register roughly: m1 API, m2 frontend (depends on m1 for the contract or independent with a shared contract), m3 tests/integration (depends on m1, m2), plus QA.
- [ ] Task 6.3: Observe the whole run: parallel spawn of independent milestones, m3 waiting, QA merging branches and running `npm test`, any fail routed back, final `<ao-plan-done>`. Record timings and any prompt weaknesses; fix prompts in `service/plan/prompts.go` or `prompt.go` and re-run until a clean pass.

### M6 verification (ao-verify)
Independent re-run of Task 6.2 on a fresh data dir; confirm the board/run view reflect each phase; `ao plan events` shows the expected sequence; final merged branch in the QA worktree passes `npm test`.
