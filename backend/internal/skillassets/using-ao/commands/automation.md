# ao automation

Manage durable recurring session automations through the daemon API.

## Syntax

```text
ao automation <subcommand> [args] [flags]
ao automations <subcommand> [args] [flags]
```

## Subcommands

---

### ao automation create

Create a recurring automation for a project.

**Syntax:**
```text
ao automation create --project <id> --name <name> --prompt <text> (--rrule <rule> | --cron <expr>) [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--project string` | Project id that owns the automation | Required |
| `--name string` | Display name | Required |
| `--prompt string` | Session task prompt | Required |
| `--rrule string` | RFC 5545 recurrence rule | Required unless `--cron` is set |
| `--cron string` | Supported five-field cron expression | Required unless `--rrule` is set |
| `--timezone string` | IANA timezone | Local zone when resolvable |
| `--kind string` | Session kind | `worker` |
| `--harness string` | Agent harness; empty uses project default | - |
| `--task-mode string` | What each run creates: `direct` (one worker session), `plan` (a Task plan), or `auto` (the orchestrator decides between a plan and one worker) | `direct` |
| `--disabled` | Create disabled | - |
| `--json` | Print JSON | - |

**Example:**

```bash
ao automation create --project my-app --name "Morning triage" --prompt "Review overnight issues" --cron "0 9 * * *" --timezone America/Los_Angeles
ao automation create --project my-app --name "Nightly cleanup" --prompt "Fix the flaky tests in CI" --cron "0 2 * * *" --task-mode auto
```

With `--task-mode plan` or `auto`, each run creates a Task named `<name> · <run time in the automation's timezone>` with the prompt as its brief, and the run's `planId` points at it. A `plan` run is planned outright; an `auto` run lets the orchestrator choose (see `ao plan triage`). The run completes when the Task's QA passes, fails when the Task fails or is cancelled, and when the orchestrator hands an `auto` Task to one worker the run follows that worker's session like a `direct` run. An orchestrator automation (`--kind orchestrator`) must be `direct` (400 `INVALID_AUTOMATION_TASK_MODE`).

---

### ao automation list

List automations.

**Syntax:**
```text
ao automation list [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--project string` | Filter by project id | - |
| `--enabled bool` | Filter by enabled state when present | - |
| `--json` | Print JSON | - |

---

### ao automation get

Show one automation.

**Syntax:**
```text
ao automation get <id> [--json]
```

---

### ao automation update

Update automation metadata, schedule, harness, or enabled state.

**Syntax:**
```text
ao automation update <id> [flags]
```

**Flags:**

| Flag | Meaning |
|---|---|
| `--name string` | New display name |
| `--prompt string` | New task prompt |
| `--rrule string` | Replace schedule with an RRule |
| `--cron string` | Replace schedule with a cron expression |
| `--timezone string` | IANA timezone |
| `--kind string` | Session kind |
| `--harness string` | Agent harness; empty uses project default |
| `--task-mode string` | `direct`, `plan`, or `auto` |
| `--enabled bool` | Enable or disable future dispatch |
| `--json` | Print JSON |

At least one update flag is required. `--rrule` and `--cron` are mutually exclusive.

---

### ao automation delete

Delete an automation and its run history. Linked sessions remain available.

**Syntax:**
```text
ao automation delete <id> [--yes]
ao automation rm <id> [--yes]
```

Without `--yes`, type the automation id when prompted to confirm.

---

### ao automation runs

List run history for one automation.

**Syntax:**
```text
ao automation runs <id> [--limit <n>] [--cursor <cursor>] [--json]
```

The table lists each run's `SESSION` and, for plan and auto runs, its `PLAN`. `--limit` must be 1–100. When a non-JSON listing prints `next cursor: ...`, pass that value to `--cursor` for the next page.
