# Board lanes and moves: §11 verification script (`ao-verify`)

Spec: `docs/superpowers/specs/2026-11-15-board-lanes-and-moves.md` (rulings in §12, R1–R32). Run in an isolated lab only: never `/tmp/ao-plans/lab3`, `e2e-data`, `~/.ao`, or ports 3001/3047.

Conventions below: `A=<lab ao binary>`, with `AO_DATA_DIR`, `AO_RUN_FILE` and `AO_PORT` exported for the lab. Project A has a local bare origin (no PR provider). Project B's origin parses as GitHub (`https://github.com/<o>/<r>.git`), and its PR facts are injected the way the existing SCM tests do. Each step names the CLI and the app check; the app check also covers dark and light themes.

| # | Setup | Act | Expect |
| --- | --- | --- | --- |
| 1 | A: worker idle, `branch_state.commits > 0`, no PR | `$A session ls --project A` | LANE `needs_you (building)`; card "Building · Awaiting review" |
| 1a | same | `$A session move <id> --to validating` | exit 1, "no pull request provider" message, card unmoved; app: drag shows Validating dimmed with the reason, drop gives the translated toast |
| 1b | same | `--to ready` | "ready: Accepted"; app card in Ready |
| 2 | A: milestone worker (plan_id set), same facts | `session ls` | stays `building` (Idle) |
| 3 | worker activity `waiting_input` | `session ls`; then answer in chat | `needs_you`, "Needs input"; leaves on the next read once active |
| 4 | working session | `--to needs_you` | `<ao-board-hold>` in the transcript; card "Paused for you"; app toast "Asked <name> to pause". Then `--to building`: `<ao-board-resume>` naming "Building", card back in Building, toast "Asked <name> to continue" |
| 5 | B: PR with external changes requested, auto-inject off | `session ls` | `needs_you (in review)`, "In review · Changes requested". `--to validating`: auto_inject_review/ci = 1 in `sessions`, a review run requested (`ao review ls`) |
| 5a | B: PR draft | `--to needs_review` | refused `pr_is_draft` |
| 5b | B: only merged PRs | `--to validating` | refused `pr_decides_ready`, no review run created |
| 6 | auto Task (`ao plan create --mode auto`) | `plan ls` now, then with the lab clock 15 min on | `queued` "Deciding", then `needs_you (building)` "Needs a decision"; `ao plan triage --plan` leaves Needs you |
| 7 | planning Task | `$A plan move <id> --to queued` | "queued: On hold", `kanbanStage` building (R27); orchestrator gets `<ao-plan-hold>`; activate it: no worker spawns over several ticks; `--to building`: `<ao-plan-resume>`, workers spawn; `ao plan events` shows two `board_move` rows |
| 7a | Task with a running milestone | `--to queued` | refused `plan_not_live`; `--to needs_you` allowed, milestone keeps running |
| 7b | held auto Task | `ao plan triage --direct` | no worker until released (event `triage_deferred`), then one worker and phase direct |
| 8 | enabled automation in A | app board | In queue card "Scheduled", next run as a short date/time; drag to Building or menu Run now: run created now and claimed (`$A automation runs`), `next_run_at` unchanged; a second run-now while it runs: 409; a disabled automation has no card |
| 9 | keyboard only | Tab to a card, Shift+F10, Move to → lane; and Space, Left/Right, Space on a focused card | same outcomes as 1 and 4; refused lanes are disabled menu items with the reason on hover/focus |
| 10 | each of the nine locales | switch language | lane labels, the seven statuses, Move to, empty hints and the refusal toasts are translated |
| 11 | copy of a pre-feature database | start the new daemon on it | migration `20261115105225340_board_requests` applies; list which cards changed lane (expected: finished no-provider workers to Needs you, Deciding Tasks to In queue/Needs you, blocked or failed Tasks and needs-review CI/conflict/changes-requested cards to Needs you, provisioning sessions to In queue) |

Notes:
- A harness may refuse the push that `<ao-board-deliver>` asks for; the agent then reports `--stuck` and the card reads "Stuck" in Needs you (R28). That is the intended path.
- Moves are not offered on another host's board, a shared project, or a cloud project or session (R32).
