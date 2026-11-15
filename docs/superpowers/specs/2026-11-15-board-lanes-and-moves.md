# Board lanes "In queue" and "Needs you", and manual card moves

Date: 2026-11-15. Status: approved for implementation by the user (coordinator: Claude Fable session, implementer: `ao-implement`, verifier: `ao-verify`). Branch: `feat/task-plans`, worktree `/Users/ceo/ao-task-plans`.

## 1. Intent

The project board is the person's operational overview. Today it has five
derived lanes (Building, Validating, In review, Ready, Archive) and every lane
after Building is decided by a pull request. Two things the person needs are
not visible as lanes: what is waiting to start, and what is waiting on them.
A finished worker in a project with no PR provider sits in Building forever
(session `ao-e2e-todo3-8` on 2026-11-14), and attention is scattered as
status phrases across lanes ("Needs input" in Building, "Changes requested" in
In review, "Deciding" in Building).

This feature adds two lanes, **In queue** and **Needs you**, and lets the
person move a card between lanes by drag or menu. A move never sets a column.
It is a command that changes a durable fact or sends the agent an instruction;
the card then lands where the facts put it. The table the user approved:

| Lane | What sits here | Agents while here | Dropping a card here means |
| --- | --- | --- | --- |
| In queue | Exists but has not started: next automation run, auto Task awaiting triage, Task on hold, session still provisioning | Waiting on a time, a dependency, or a decision | Hold: do not start until moved again |
| Building | Session with no PR; Task planning or implementing | Workers code and test; orchestrator plans, spawns, triages | Start now |
| Validating | PR in an AO loop; Task in QA | AO reviews, fixes CI, addresses its own comments; QA agent tests | Push, open the PR, trigger AO review |
| In review | PR waiting on a person's review or answer | Wait; address new human comments | Stop AO's loop, hand the PR to a person |
| Needs you | Waiting on a person, whatever its stage | Blocked; a notice was sent | Pause and ask me: stop and summarise |
| Ready | PR merged, closed, mergeable or approved; Task with QA passed | Nothing | Accept (only when no PR can speak for it) |
| Archive | Terminated sessions, cancelled Tasks | Nothing | (not a drop target; the archive is a modal) |

## 2. Goals, non-goals, anti-goals

Goals
- The person sees, per project, what has not started and what needs them, in two lanes.
- Every lane boundary is a command the agents already know how to run, reachable by drag and by keyboard/menu.
- Columns stay derived (AGENTS.md hard rule: no stored display status). Only durable intents are persisted.
- A card refused a move says why, in the person's language.

Non-goals
- Per-milestone queue cards. Queued milestones stay in the Task run view's DAG. (Coordinator's call; the user left this open.)
- Reordering cards inside a lane. Lane order stays attention-first, then most recently updated.
- Dragging to Archive. Terminate and Cancel stay in the card menu.
- Cloud (shared) projects. Plans are already disabled there; moves are too.

Anti-goals
- A drag that silently does nothing. Either the facts change and the card lands somewhere, or the drop is refused with a reason.
- A move that destroys work: no move kills a session, deletes a branch, or cancels running milestones.
- Pulsing or animating many cards. DESIGN.md §9 and §10 stand.

## 3. Lane model

### 3.1 Column enum

`contract.KanbanColumn` gains two values. Order on the board, left to right:

```
queued, building, validating, needs_review, needs_you, ready
```

`archive` stays a column (terminated) but is not a lane; the archive modal is
unchanged. `boardKanbanColumnOrder` in `packages/product-ui/src/session-presentation.ts`
is the single place the order lives; `SessionsBoardGridView` renders
`grid-cols-6` with `min-w-[96rem]` and keeps `xl:min-w-0`. (Lane placement of
Needs you next to Ready is the coordinator's call; it is one array to change.)

Labels (English, `column.*` in both `packages/product-ui/src/session-presentation.ts`
and `frontend/src/renderer/i18n/*.json`, all nine locales translated, not
placeholders): `column.queued` = "In queue", `column.needs_you` = "Needs you".

Lane tokens (`kanbanColumnBases`):
- `queued`: dot `var(--color-status-terminated)`, no glow, `text-passive` title, `bg-status-terminated` dot class. Muted, per DESIGN.md §5 "Idle / unknown".
- `needs_you`: dot `var(--color-status-needs-you)`, `dotGlow: true`, `text-status-needs-you`, `bg-status-needs-you`. The only lane that may use the needs-human colour on its header.

`DISPLAY_STATUSES` in `session-models.ts` and `contract.DisplayStatus` gain:
`"Starting"`, `"On hold"`, `"Scheduled"`, `"Paused for you"`, `"Needs a decision"`, `"Stuck"`, `"Accepted"`.

### 3.2 New facts the reducer reads

`contract.KanbanSessionFacts` gains:

```go
// HasPRProvider reports that the session's repository origin parses as a
// provider AO can open pull requests on (scmProvider.ParseRepository ok).
HasPRProvider bool
// Commits is branchState.commits: commits on the branch past its base.
Commits int
// PlanManaged reports a milestone or research worker of a Task plan: its
// delivery is the plan's, never its own.
PlanManaged bool
// Provisioning reports provision_state == provisioning.
Provisioning bool
// LatestReport is the state of the newest ao report for the session, or "".
LatestReport ReportState // "", "checkpoint", "needs_input", "stuck", "done"
// BoardRequest is the person's durable intent from a manual move.
BoardRequest BoardRequest // "", "attention", "accepted"
```

`contract.KanbanPresentation` gains `Stage KanbanColumn`. Stage is the lifecycle lane whenever Column was decided by a board request or by Needs you; otherwise it equals Column.

`ReportState` and `BoardRequest` are new string types in `backend/pkg/contract/kanban.go`
mirroring `domain.ReportState` and the new `domain.BoardRequest`.

### 3.3 Session derivation (`contract.DeriveKanbanPresentation`)

In order; the first match wins:

1. `IsTerminated` → `archive`, "Terminated". (unchanged)
2. `BoardRequest == accepted && len(prs) == 0` → `ready`, "Accepted".
3. `Provisioning` → `queued`, "Starting".
4. Compute the lifecycle placement exactly as today (artifact → validating; no PR → building; PR → per-PR ranking). Call it `stage`, `status`.
5. `BoardRequest == attention` → `needs_you`, "Paused for you", `Stage = stage`.
6. `needsYou(stage, status, facts, prs)` true → `needs_you`, with the status it returns, `Stage = stage`.
7. Otherwise `stage`, `status`.

`needsYou` is true when any of:
- `status` ∈ {"Blocked", "Needs input"} (the worker asked or is blocked). Keep the status.
- `status` ∈ {"CI failing", "Merge conflicts", "Changes requested"} and `stage == needs_review` (no AO loop is fixing it). Keep the status. This is the same set `sessionNeedsYou` in `frontend/src/renderer/lib/session-presentation.ts` already uses for the sidebar.
- `LatestReport ∈ {needs_input, stuck}` and `Activity ∈ {idle, exited}` → "Needs input" / "Stuck".
- Finished with nowhere to deliver: `stage == building`, `Activity ∈ {idle, exited}`, `Commits > 0`, `len(prs) == 0`, `!HasArtifactOutput`, `!HasPRProvider`, `!PlanManaged`, `!Interrupted` → "Awaiting review" (reuse `DisplayAwaitingReview`).

Not Needs you: "Exited", "No signal", "Interrupted", "Commented", "Awaiting review" with a PR. Those stay where they are today. A plan-managed worker is never Needs you for delivery reasons; its question/blocked states still are.

### 3.4 Task (plan) derivation (`domain.DerivePlanKanban`)

Signature becomes `DerivePlanKanban(plan Plan, phase PlanPhase, ms []Milestone, now time.Time) (contract.KanbanColumn, contract.DisplayStatus, contract.KanbanColumn /*stage*/)`.

1. `cancelled` → archive, "Cancelled". `direct` → archive, "One worker" (card hidden, unchanged).
2. `plan.BoardRequest == hold` → `queued`, "On hold"; stage = lifecycle lane below.
3. `plan.BoardRequest == attention` → `needs_you`, "Paused for you"; stage = lifecycle lane.
4. `triage`, undecided: `now - CreatedAt < plan.TriageReminderAfter()` → `queued`, "Deciding"; else → `needs_you`, "Needs a decision", stage `building`. (`TriageReminderAfter` moves from `service/plan/triage.go` to `domain` so the reducer can read it.)
5. `failed` → `needs_you`, "Needs you", stage `needs_review`. (Today: needs_review/"Needs you".)
6. Any milestone `blocked` in a live phase → `needs_you`, "Blocked", stage `building`. (Today: building/"Blocked".)
7. `done` → ready, "QA passed". `qa` → validating, "Testing". `fixing` → building, "Fixing QA findings". `planning` → building, "Planning". `implementing` → building, "Implementing d/t". (unchanged)

`PlanSummary` gains `kanbanStage`.

### 3.5 Automation cards (queue only)

An enabled automation whose project is the board's project appears in
In queue as one card, id `automation:<id>`, title = `displayName`, display
status "Scheduled", with the next run time (`nextRunAt`, formatted with the
card's `formatTime`) on the metadata line where a session shows its updated
time. It is built client-side from `useAutomations()` (already includes
`projectId`, `enabled`, `nextRunAt`, `taskMode`); no new list endpoint.
Disabled automations do not appear. A run that is `spawning`/`running` is
represented by the session or Task it created, not by the automation card.

Reason: `service/automation/scheduler.go:113` materialises a run only once
`NextRunAt` has passed, so there is no pending row to show ahead of time.

## 4. Durable intents

Migration (created with `npm run migration:create -- board_requests`, ID appended to `shippedMigrations` in `migrate_burned_versions_test.go`):

```sql
ALTER TABLE sessions ADD COLUMN board_request TEXT NOT NULL DEFAULT ''
    CHECK (board_request IN ('', 'attention', 'accepted'));
ALTER TABLE sessions ADD COLUMN board_request_at TIMESTAMP;
ALTER TABLE plans ADD COLUMN board_request TEXT NOT NULL DEFAULT ''
    CHECK (board_request IN ('', 'hold', 'attention'));
ALTER TABLE plans ADD COLUMN board_request_at TIMESTAMP;
```

`domain.BoardRequest` string type with constants `BoardRequestNone ""`,
`BoardRequestHold "hold"`, `BoardRequestAttention "attention"`,
`BoardRequestAccepted "accepted"`. `SessionRecord` and `Plan` gain
`BoardRequest` and `BoardRequestAt *time.Time`. sqlc queries:
`SetSessionBoardRequest`, `SetPlanBoardRequest`.

These are facts about what the person asked for, not display state. The
column is still derived at read time. A request is cleared by the opposite
move or, for `accepted`, when a PR later appears on the session (the PR then
speaks for it; clearing happens in the same place `OutputType` is reconciled,
`lifecycle.Manager.ReconcileSessionOutputType`).

## 5. Moves

### 5.1 Session: `POST /sessions/{sessionId}/board-move`

Request `BoardMoveRequest { to: KanbanColumn }`. Response 200
`SessionResponse` (fresh read model). Errors: 400 `INVALID_BOARD_COLUMN`
(unknown or `archive`), 404 `SESSION_NOT_FOUND`, 409 `BOARD_MOVE_UNSUPPORTED`
with `details.reason` from the enum below, 409 `SESSION_TERMINATED`.

Let `from` be the session's current derived column and `stage` its lifecycle
lane. The service applies, in this order:

1. If `from == needs_you` and `BoardRequest == attention`: clear the request and send the agent `<ao-board-resume>` (§6). If `to == stage`, stop here (this is "handled, continue").
2. Then apply the target rule:

| `to` | Rule |
| --- | --- |
| `queued` | Refused, reason `session_cannot_queue` (a live session cannot be un-started). |
| `building` | From `needs_you` only (handled by step 1). Otherwise refused, `already_building` if `stage == building`, else `cannot_restart`. |
| `validating` | No PR: requires `HasPRProvider`, else refused `no_pr_provider`. Send `<ao-board-deliver>` (§6). With a PR in `needs_review`: run the same operation as `ao review trigger` for the PR (service `review`), and set `auto_inject_review` and `auto_inject_ci` true. Already validating: refused `already_there`. |
| `needs_review` | Requires a PR, else refused `no_pr`. A draft PR is refused `pr_is_draft` (R17). Set `auto_inject_review=false`, `auto_inject_ci=false`, `auto_review_enabled=false` (the facts `derivePRKanbanColumn` reads), and cancel a running AO review pass for the current head if one exists. |
| `ready` | Requires `len(prs) == 0`, else refused `pr_decides_ready`. Set `BoardRequest = accepted`. |
| `needs_you` | Set `BoardRequest = attention`; send `<ao-board-hold>` (§6). Already there by facts (not by request): still set the request so the card stays put after the agent answers. |

Reason enum (`BoardMoveRefusal`): `session_cannot_queue`, `already_there`,
`already_building`, `cannot_restart`, `no_pr_provider`, `no_pr`,
`pr_decides_ready`, `plan_cannot_validate`, `plan_decides_ready`,
`plan_not_live`, `automation_only_runs`, `pr_is_draft` (R17).

### 5.2 Task: `POST /plans/{planId}/board-move`

Same request shape. Response `PlanResponse`. 404 `PLAN_NOT_FOUND`, 409
`PLAN_NOT_LIVE` for cancelled/done/direct plans.

1. If `BoardRequest != ""` and `to != queued && to != needs_you`: clear the request, wake the scheduler, send the orchestrator `<ao-plan-resume>`. If `to == stage`, stop.
2. Target rule:

| `to` | Rule |
| --- | --- |
| `queued` | Allowed in `triage`, `planning`, or `implementing` with no `running` milestone. Set `hold`. Otherwise refused `plan_not_live`. |
| `building` | Only as the release of a hold/attention (step 1). Otherwise `already_there`. |
| `validating` | Refused `plan_cannot_validate`: QA starts when the last implement milestone is done. |
| `needs_review` | Refused `plan_cannot_validate`. |
| `ready` | Refused `plan_decides_ready`. |
| `needs_you` | Set `attention`; send `<ao-plan-hold>` to the orchestrator. |

While `BoardRequest != ""` the plan scheduler does not spawn milestone
workers, does not run the direct spawn of a triage decision, and does not
send the triage reminder. Decisions and `activate` are still recorded
(state changes) and act when the request clears, reusing the deferred-kickoff
path (`RetryDeferredKickoff`). Running milestones continue; the QA loop
continues.

### 5.3 Automation: `POST /automations/{automationId}/run-now`

Response 202 `AutomationRunSummaryResponse` for a run with
`scheduled_for = now` and status `pending`; the scheduler is woken and claims
it on its next tick. 409 `AUTOMATION_DISABLED` when `enabled == false`, 409
`AUTOMATION_RUN_ACTIVE` when a run is `spawning`/`running`. The automation's
`nextRunAt` is unchanged (the scheduled occurrence still happens). The UI
sends this for a drop from `queued` to `building`; every other target for an
automation card is refused client-side with `automation_only_runs`.

### 5.4 CLI (thin client, `backend/internal/cli`)

- `ao session move <id> --to <lane>`; `ao plan move [<plan-id>] --to <lane>`; lanes are the enum values. On 409 print the daemon's message.
- `ao automation run-now <id>`.
- `ao session ls` and `ao plan ls` show the new columns and, for `needs_you`, append the stage in parentheses: `needs_you (in review)`.

## 6. Agent notices

Delivered with `session.Service.Send` (Chat queues for an idle worker; TUI
injects). Plain text wrapped in the tag, no secrets, under 1 KB.

```
<ao-board-hold>
The person moved this session to "Needs you" on the board. Finish the step you are in, do not start new work, then reply in two to five lines: what is done, what is in progress, what you need from them. Then wait.
</ao-board-hold>

<ao-board-resume>
The person moved this session back to the board's "<lane label>" lane. Continue the task from where you stopped.
</ao-board-resume>

<ao-board-deliver>
The person moved this session to "Validating". Push your branch to origin, open a pull request against <base branch>, then run `ao review trigger --pr <url>`. If the push or PR fails, run `ao report --stuck --note <why>`.
</ao-board-deliver>

<ao-plan-hold>
The person put Task <id> "<title>" on hold from the board. Do not spawn or send to workers for this Task until an <ao-plan-resume> notice arrives. Running milestones keep going.
</ao-plan-hold>

<ao-plan-resume>
Task <id> "<title>" is released. Continue.
</ao-plan-resume>
```

`<ao-plan-hold>` is also sent for a Task moved to Needs you (the orchestrator
is told to summarise, same two-to-five-line ask as the session hold). Each
notice is recorded as a plan event kind `board_move` with the lane in the note.

## 7. App

Files: `packages/product-ui/src/SessionsBoardView.tsx`, `session-models.ts`,
`session-presentation.ts`; `frontend/src/renderer/components/SessionsBoard.tsx`,
`SessionsBoardAdapters.tsx`; new `frontend/src/renderer/hooks/useBoardMove.ts`.

- **Drag.** `SessionsBoardGridView` wraps the grid in `DndContext` from `@dnd-kit/core` (already a dependency; pattern in `Sidebar.tsx:2671`) with `PointerSensor` (distance 6) and `KeyboardSensor`. Each card wrapper is `useDraggable`; each lane is `useDroppable`. New props: `onMove?: (item, to) => void`, `canMove?: (item, to) => BoardMoveRefusal | null`. While a drag is active, lanes that `canMove` refuses render at reduced opacity and show the refusal as the lane's hint line; dropping there does nothing beyond a toast. The card snaps back; it moves only when the daemon's next read model places it (SSE/CDC already drives the board). During the round-trip the card shows the working spinner in its status slot. No optimistic column change.
- **Menu.** Every card's `⋯` menu gains "Move to" with the allowed lanes (refused ones disabled with the reason as tooltip). This is the keyboard path.
- **Needs you cards.** The status line prints the stage label in muted text before the reason: `In review · Changes requested`. One line, no badge, no second colour (DESIGN.md §9). `kanbanStage` comes from the daemon.
- **Automation cards.** `BoardAutomationCardAdapter`: title, "Scheduled" status, next run time, avatar from the automation's harness, menu: Run now, Open automations. `isBoardAutomationItem` like `isBoardPlanItem`.
- **Refusals.** Toast `board.move.refused.<reason>` per enum value, e.g. `no_pr_provider`: "This project has no pull request provider, so there is nothing to validate. Accept the work from Ready instead."
- **Empty lane hints.** `queued`: "Nothing waiting to start." `needs_you`: "Nothing needs you." Shown under the header only when the lane is empty, `text-passive`.
- **i18n.** New keys in all nine locales, translated: the two column labels, seven display statuses (`displayStatus.*`), eleven refusal reasons, "Move to", "Run now", two empty hints, the notice-free hold/resume toasts ("Asked <name> to pause", "Asked <name> to continue").
- **DESIGN.md.** §9 lane sentence becomes: "The canonical lanes are In queue, Building, Validating, In review, Needs you, and Ready; archive is a separate history state." Add a decisions-log row dated 2026-11-15: "Two cross-cutting lanes (In queue, Needs you) and manual moves as commands, not stored columns | Status stays derived; a move changes a fact or instructs the agent."

## 8. Docs

- `frontend/src/docs/content/guides/board.mdx` (or the existing board page if one exists): the seven-row table from §1, what each drop does, refusal reasons.
- `cli.mdx`: `ao session move`, `ao plan move`, `ao automation run-now`.
- `guides/task-plans.mdx`: hold and Needs you for Tasks; triage waits in In queue, then Needs you after 15 minutes.
- Troubleshooting entry: "A finished session stays in Building" → it is now in Needs you as Awaiting review when the project has no PR provider; accept it from the card menu.

## 9. Tests

Go
- `backend/pkg/contract/kanban_test.go`: table tests for every §3.3 rule and the precedence order; a plan-managed idle worker with commits and no provider stays `building`; an accepted session that later gets a PR returns to the PR lane.
- `backend/internal/domain/plan_test.go`: §3.4 rows incl. triage before/after reminder, hold, attention.
- `service/session`: board-move matrix (each `to` × {no PR / PR validating / PR needs_review / needs_you by fact / needs_you by request}), notices sent with exact tags, auto-inject flags toggled, 409 reasons.
- `service/plan`: hold pauses spawning (scheduler tick with a ready milestone spawns nothing), release spawns; triage decision while on hold defers the direct spawn; notices recorded as events.
- `service/automation`: run-now creates a pending run at now, 409s, scheduler claims it on wake.
- `httpd`: route/spec parity, DTO enums include the new columns; migration in burned-versions test.
- CLI: `move` and `run-now` wire tests.

Frontend
- `packages/product-ui`: six lanes render in order; `toKanbanColumn` accepts new values; column views have labels/tokens; drag end calls `onMove` with `(item, to)`; refused lane renders disabled; keyboard sensor moves a card.
- `SessionsBoard.test.tsx`: automation cards appear in In queue for the project and not when disabled; Needs you card prints `stage · reason`; a refused drop shows the toast key.
- i18n test: every new key present in all nine locales and none equal to the English string except where the language genuinely matches.

## 10. Milestones for `ao-implement`

B1 Lane model: §3 (contract + domain derivation, `Stage`, product-ui enum/labels/tokens/order, grid to six lanes, DTO enums, `npm run api`). Gate: Go + product-ui tests green; board renders six lanes with existing cards placed by the new rules.
B2 Intents and moves: §4, §5, §6 (migration, services, endpoints, scheduler pause, notices, CLI). Gate: service matrix tests; `ao session move`/`ao plan move`/`ao automation run-now` against a dev daemon.
B3 Board UI: §7 (drag, menu, automation cards, Needs you status line, refusals, i18n, DESIGN.md). Gate: product-ui and renderer tests; manual check in the desktop lab.
B4 Docs §8 and a §11 script for `ao-verify`.

Verification by `ao-verify` after B2 and after B4, then the user's app (lab3) is restarted on the verified build.

## 11. Verification scenarios (`ao-verify`)

Isolated lab (never `/tmp/ao-plans/lab3` or `e2e-data`). Local bare-origin project A (no provider) and a fixture project B whose origin parses as GitHub (no network; PR facts injected via the existing SCM observer fakes where tests already do so).

1. Finished worker, project A: idle, commits, no PR → Needs you, "Awaiting review", stage Building. Drag to Ready → "Accepted" in Ready. Drag to Validating → refused `no_pr_provider` toast, card unmoved.
2. Milestone worker, project A, same facts → stays Building (plan-managed).
3. Worker asks a question (activity waiting_input) → Needs you, "Needs input"; answer in chat → leaves Needs you on the next read.
4. Drag a working session to Needs you → `<ao-board-hold>` delivered (visible in transcript), card "Paused for you"; agent summary arrives; drag back to Building → `<ao-board-resume>`, card returns to Building.
5. Project B session with PR and changes requested, auto-inject off → Needs you with stage In review; drag to Validating → auto-inject flags on and a review run requested.
6. Auto Task created → In queue "Deciding"; after 15 min (clock advanced in the lab) → Needs you "Needs a decision"; `ao plan triage --plan` → leaves Needs you.
7. Task on hold: drag a planning Task to In queue → "On hold"; orchestrator activates it; no worker spawns; drag to Building → workers spawn.
8. Enabled automation → In queue card "Scheduled <time>"; drag to Building → run created now and claimed; disabled automation → no card.
9. Keyboard-only: Tab to a card, open menu, "Move to" → same outcomes as 1 and 4.
10. All nine locales: lane labels and the refusal toast translated.
11. Migration applies on a copy of a pre-feature database; existing cards keep their lanes except the §3.3/§3.4 reclassifications, which are listed in the report.

## 12. Open rulings (coordinator's calls, revisable)

- R1 Needs you sits between In review and Ready. Change `boardKanbanColumnOrder` if the user prefers it leftmost.
- R2 Exited alone is not Needs you. A restart decision is a card-menu action; adding every exited worker to Needs you would flood it.
- R3 Queue cards are Tasks, automations, and provisioning sessions only. Milestones stay in the run view.
- R4 A live session cannot be dropped on In queue. Pausing a running agent is the Needs you move.
- R5 A Task cannot be dragged into Validating or Ready. QA decides both.
- R6 `accepted` is cleared automatically when a PR appears; the PR then decides the lane.
- R7 "Commented" and "Awaiting review" with a PR stay in In review. They are the review loop's normal waiting, not an interruption.

### Implementer's calls (`ao-implement`, revisable)

- R8 The triage clock in §3.4 step 4 reads `plan.CreatedAt`, as written. The reminder itself still counts from the kickoff send; the two differ by the kickoff latency (seconds), and the reducer cannot read plan events.
- R9 "Finished with nowhere to deliver" includes `Activity == exited` as §3.3 says. R2 still holds for an exited worker with no commits, or one in a project with a provider: it stays "Exited" where it is.
- R10 `accepted` with a PR: the reducer already ignores it (§3.3 rule 2 needs `len(prs) == 0`), so correctness does not depend on the clear. The clear is hygiene and runs where a PR is first attached to the session as well as in `ReconcileSessionOutputType`.
- R11 `LatestReport` is read for all sessions of a list in one query (newest report per session), not `ListReportsBySession` per card.
- R12 A Task in Needs you by fact (failed, blocked, needs a decision) with no request, dropped on Building, is refused `cannot_restart`, not `already_there`: the card is not in Building and the person must act on the reason first.
- R13 `run-now` also returns 409 `AUTOMATION_RUN_ACTIVE` when a `pending` run already exists for the automation, so a double drop cannot queue two runs.
- R14 Moving a PR to Validating triggers the review with `Rerun: true` so an already reviewed head is reviewed again. A trigger error (no reviewer harness, review already running) is returned as the 409 message; the inject flags are set first and stay set.
- R15 The sidebar's `sessionNeedsYou` set is unchanged; aligning it with the lane is a separate change.
- R16 A dev daemon for the B2 gate runs with its own `AO_DATA_DIR` under the scratchpad and a free port that is neither 3001 nor 3047. (Coordinator) Every dev-daemon environment also sets `GH_TOKEN` and `AO_GITHUB_TOKEN` to a value containing a newline, so Go refuses the header before dialing and no lab reaches api.github.com with the user's keychain token.
- R17 (coordinator) A draft PR dropped on In review is refused `pr_is_draft`: "This pull request is a draft. Mark it ready for review on the provider first." A board move never writes to the provider.
- R18 Every refusal is decided before any side effect, so a refused move changes nothing. A card paused by request and dropped on Building or on its own stage is the release alone (clear, resume notice), for sessions and Tasks alike.
- R19 A notice that cannot be delivered (controller down) does not undo the move: the fact is recorded and the failure logged.
- R20 Validating with a live PR in any stage other than Validating (In review, Ready, or Needs you by fact) runs the same review trigger. A failed trigger is a 409 with reason `review_trigger_failed`; the inject flags stay on (R14). (Coordinator refinement) A session whose PRs are all merged or closed is refused `pr_decides_ready` before any trigger call.
- R21 In review on a card whose stage is already In review (shown in In review or in Needs you by fact) is refused `already_there`: no AO loop is left to stop. Only when every live PR is a draft is it `pr_is_draft` (R17).
- R22 A direct triage decision on a held Task is recorded on the plan (state stays triage, decision set, event `triage_deferred`) and its worker starts on release. A second decision is refused as for any decided Task.
- R23 A hold also pauses the deferred-kickoff retry; release retries it. A person's plan decision on a held Task still sends the plan request: planning is not spawning.
- R24 `POST /plans/{planId}/board-move` answers with `PlanDetailResponse`, as `activate` does, rather than `PlanResponse`.
- R25 `ao session ls` and `ao plan ls` gain a LANE column.
- R26 The mobile app is unchanged; it maps unknown lanes through its existing status fallback.
- R27 (coordinator) Stage is the lifecycle lane whenever a board request places the card (a held Task's stage is building; an accepted session's is building), per the reworded §3.2.
- R28 (coordinator) A worker's own harness permission rules may block the push that `<ao-board-deliver>` asks for. The agent then files `ao report --stuck` as instructed and the card reads Stuck in Needs you: the intended path, not a defect.
- R29 Drag and drop lives in the renderer (`components/BoardMoves.tsx`), not in `SessionsBoardGridView`: product-ui's import boundary allows only clsx, motion, react and tailwind-merge. product-ui draws the drag through `laneState` (drop ref, refused-lane dimming with the reason as hint, hover) and `labels.emptyLane`. The drag/keyboard behaviour tests are renderer tests.
- R30 The card has no ⋯ button; its menu is the right-click/context-menu (Shift+F10 on the focused card), which gains "Move to". Space on the focused card starts a keyboard drag; Left/Right step between lane centres; Enter still opens the card.
- R31 An automation card's next run is an absolute short date and time (as the Automations page shows it), not the card's relative `formatTime`, which reads future times badly.
- R32 Moves are offered on this machine's boards only: not on another host's board, a shared project, or a cloud project or session.
- R33 (coordinator) An accepted session dropped on Building clears its `accepted` request with no notice to the agent, and the card returns to where its facts put it, as dragging out of Needs you clears `attention`. Every other drop of an accepted card follows the ordinary rules (Ready is `already_there`).
- R34 (ao-verify finding 1) `SessionResponse` and `PlanSessionSummary` carry `hasPrProvider`, the same fact the reducer reads. The client predicts `no_pr_provider` from it, so Validating dims with the reason during a drag and the Move to item is disabled. On `PlanSessionSummary` it is omitted when false; on the session it is always present, and a daemon that omits it is treated as unknown.
- R37 (ao-verify finding 4) A card in the Needs you lane gets neither the attention pulse nor the tint; the lane header's needs-you colour and the status line carry the attention. The pulse and tint stay only for a card outside Needs you that still reads as attention.
- R38 (ao-verify finding 5) The In queue lane title, a queued Task's status and the automation card's "Scheduled" use `text-muted-foreground` instead of `text-passive`; the dot keeps `bg-status-terminated`. Decided from the dark tokens rather than a desktop capture: passive (oklch L 0.48) on the dark background (L 0.21) is about 2.7:1, muted-foreground (L 0.72) about 7:1. Light theme: muted-foreground (L 0.48) on L 0.985 is about 7:1 too.
- R36 (ao-verify finding 3) A Task card's status goes through `displayStatus.*` keys in all nine locales (`lib/plan-status.ts`): Planning, Deciding, Implementing d/t (with its counts), Fixing QA findings, Testing, QA passed, Needs you, Blocked, On hold, Needs a decision, Paused for you, Cancelled, One worker. An unknown phrase from a newer daemon prints as sent.
- R35 (ao-verify finding 2) A focused card opens its own menu on Shift+F10 and the ContextMenu key (Chromium on macOS sends no contextmenu for either). Closing the menu returns focus to the card, and a move from the menu refocuses the card where it lands. A refused lane's reason is muted text under its disabled item, since disabled items take no keyboard focus. The drag's screen-reader instructions and announcements are translated and name the card title and lane (`board.drag.*`). Supersedes the tooltip in R30.
- R39 (coordinator, B6) The finished-worker rule in §3.3 depends on parsing the project's origin. The GitHub provider read any remote with no `://` and one slash as `owner/repo` shorthand, so a local bare origin like `/tmp/ao-e2e-todo3.git` became github.com/tmp/ao-e2e-todo3: `hasPrProvider` was true, and the SCM observer polled api.github.com for it every 30 s with the person's token. The shorthand now rejects anything that looks like a filesystem path (a leading `/`, `./`, `../` or `~`, a backslash, a Windows drive, a `.git` suffix). The GitLab provider has no shorthand branch and already rejects local paths.
