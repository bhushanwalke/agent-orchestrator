# ao plan

Plans are what the desktop app calls Tasks: a ticket the project orchestrator turns into milestones. AO then runs one worker per milestone in dependency order, and runs a QA worker once every milestone is done. The orchestrator is the planner. Inside a plan session, `<plan-id>` may be omitted because `AO_PLAN_ID` supplies it.

```text
ao plan create --project <id> --title <t> (--brief <text> | --brief-file <path>) [--ticket <ref>] [--max-parallel N] [--json]
ao plan ls [--project <id>] [--json]
ao plan get [<plan-id>] [--json]
ao plan set-spec [<plan-id>] --file <path>
ao plan milestone add [<plan-id>] --key <k> --title <t> (--description <text> | --description-file <path>) [--kind implement|qa] [--depends-on a,b] [--sparse <dir>]...
ao plan milestone update [<plan-id>] <key> [same flags; only the ones you pass change]
ao plan milestone rm [<plan-id>] <key>
ao plan milestone retry [<plan-id>] <key>
ao plan activate [<plan-id>]
ao plan cancel [<plan-id>] [--yes]
ao plan qa-result [<plan-id>] (--pass | --fail --milestone <key>) --note <text>
ao plan events [<plan-id>] [--after N] [--limit N] [--json]
```

## When the orchestrator receives `<ao-plan-request id="PLAN">`

1. Investigate the ticket and the code. Spawn research workers in parallel when the ticket is broad.
2. Write the plan as Markdown and store it with `ao plan set-spec PLAN --file plan.md`.
3. Register milestones with `ao plan milestone add`. Each one should be a single PR that a careful engineer can finish in a few hours. Declare `--depends-on` only when a milestone needs another milestone's code. Add a dependency's key before the milestone that depends on it. List `--sparse` directories only when you know which directories the milestone touches and builds.
4. Run `ao plan activate PLAN`. Activation rejects an invalid graph with `PLAN_GRAPH_INVALID`: no implement milestone, a cycle, or a dependency on `qa`. Fix the milestones and activate again. Activation adds the `qa` milestone automatically.

Milestones can be edited only while the plan is `planning`; later edits return `PLAN_NOT_EDITABLE`. Limits:

- 20 milestones per plan.
- Keys match `^[a-z0-9][a-z0-9-]{0,31}$`, and `qa` is reserved.
- Up to 50 sparse paths per milestone.
- `--max-parallel` is 1-10 and defaults to 3. It stays editable through `PATCH /api/v1/plans/{id}`.

Removing a milestone that another one depends on returns `MILESTONE_HAS_DEPENDENTS`.

## Milestone workers

A milestone worker has `AO_PLAN_ID` and `AO_MILESTONE_KEY` set, and its prompt lists the dependencies that are already done with their branches.

1. Merge each dependency's branch into your branch before you start (`git merge <branch>`).
2. Stay inside your milestone. Ask the orchestrator with `ao send` when the plan needs changing.
3. Report `ao report --checkpoint` for progress and `ao report --stuck` when blocked; the milestone shows blocked until your next checkpoint or done report.
4. Build and run the tests that cover your change, commit, open the PR as the project rules allow, then `ao report --done --note "<what you built, how you verified it>" [--pr-created <url>]`. The scheduler advances only on done reports.
5. When you report done, AO records the branch your work is on: `--branch <name>` if you pass it, otherwise the branch checked out in your worktree. Dependent milestones and QA merge that branch, so stay on the branch you committed to.

A message wrapped in `<ao-qa-defect>` is QA's findings for your milestone: fix them on your current branch, rerun the tests, push to your PR if one exists, and report done again.

If a worker's session ends without a done report, AO starts a new worker for the milestone, up to 3 attempts. A worker whose agent keeps failing (the thread reports a system error, or its last two turns failed) is marked blocked with the reason.

## QA sessions

Only the plan's QA session may run `ao plan qa-result`, and the session id comes from `AO_SESSION_ID`.

1. Merge every milestone branch from your prompt into your branch, in the order listed.
2. Build, run the full test suite, and exercise the feature against the ticket and the plan spec (`ao preview` and `ao browser` for web apps).
3. Record one `--fail --milestone <key> --note "<steps, expected, actual, file hints>"` per milestone that has defects, putting all of that milestone's defects in one note. Use `--pass --note "<what you verified>"` when everything works. A round that recorded a failure cannot also pass (`PLAN_QA_HAS_FAILURES`).
4. Finish with `ao report --done --note "<summary>"`. Do not open pull requests and do not push.

A done report without a recorded verdict is not a pass: AO asks the session for a verdict until one is recorded. Use `--fail --milestone ""` only when no milestone owns the failure; that blocks QA and forwards the note to the orchestrator.

After per-milestone failures, each failing milestone goes back to its worker (the same session if it is still running, otherwise a fresh worker on the milestone's branch), and a new QA session runs once every fix is done. When QA passes, the orchestrator receives `<ao-plan-done id="PLAN">` with each milestone's branch and PRs; summarise them for the human.

## Watching, retrying, cancelling

- `ao plan get` shows the phase, each milestone's status and session, and the latest events. `ao plan events --after N` follows the log.
- `ao plan milestone retry PLAN <key>` gives a failed milestone (or a blocked QA) a fresh 3 attempts. `ao plan milestone retry PLAN qa` after QA passed re-runs QA on a new session, as the app's Re-run QA does.

`ao plan cancel` asks for confirmation when run interactively and needs `--yes` otherwise. Cancelling leaves the plan's sessions running. Daemon errors keep their API code and request id.
