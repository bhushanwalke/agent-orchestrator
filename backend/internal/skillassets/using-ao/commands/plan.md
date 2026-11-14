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

## QA sessions

Only the plan's QA session may run `ao plan qa-result`, and the session id comes from `AO_SESSION_ID`. Record one `--fail --milestone <key>` per milestone that has defects, putting all of that milestone's defects in one note. Use `--pass` when everything works. Use `--fail --milestone ""` only when no milestone owns the failure; that blocks QA and forwards the note to the orchestrator.

`ao plan cancel` asks for confirmation when run interactively and needs `--yes` otherwise. Cancelling leaves the plan's sessions running. Daemon errors keep their API code and request id.
