package plan

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Plan session roles carried in ports.PlanSpawnContext.Role.
const (
	RoleImplementer = "implementer"
	RoleQA          = "qa"
)

// QASpecClipBytes bounds the plan document quoted in the QA prompt.
const QASpecClipBytes = 8000

// PromptNoteMaxBytes bounds a report or QA note as events store it and as
// the QA prompt and the done summary quote it.
const PromptNoteMaxBytes = 900

// doneNoteRule asks workers to lead with the verification, which clipping
// would otherwise drop from the end of a long note.
const doneNoteRule = "Start your done note with what you verified (tests run, counts), then what you built."

// clipNote shortens a note to at most limit bytes, ending at the last
// sentence end before the limit, else the last word, and marks the cut.
func clipNote(note string, limit int) string {
	if len(note) <= limit {
		return note
	}
	const mark = " …"
	head := truncateBytes(note, limit-len(mark))
	if at := lastSentenceEnd(head); at >= len(head)/2 {
		return head[:at] + mark
	}
	if at := strings.LastIndexAny(head, " \n\t"); at > 0 {
		return strings.TrimRight(head[:at], " \n\t") + mark
	}
	return head + mark
}

// lastSentenceEnd is the index just past the last ". ", "! ", or "? " (or
// the same before a newline) in s, or -1.
func lastSentenceEnd(s string) int {
	for i := len(s) - 2; i >= 0; i-- {
		switch s[i] {
		case '.', '!', '?':
			if s[i+1] == ' ' || s[i+1] == '\n' {
				return i + 1
			}
		}
	}
	return -1
}

// displayNameMaxChars matches the spawn display name limit.
const displayNameMaxChars = 100

// depInfo is one done dependency as an implementation worker sees it.
type depInfo struct {
	Key, Title string
	Branch     string
	SessionID  domain.SessionID
}

// builtInfo is one implementation milestone as the QA worker and the plan
// done summary see it.
type builtInfo struct {
	Key, Title string
	Branch     string
	SessionID  domain.SessionID
	PRs        []string
	Note       string
}

func ticketLine(p domain.Plan) string {
	if strings.TrimSpace(p.TicketRef) == "" {
		return "(none)"
	}
	return p.TicketRef
}

// milestoneDisplayName is "{plan} · {key}: {title}" (or "{plan} · QA"),
// truncated to the spawn display name limit.
func milestoneDisplayName(p domain.Plan, m domain.Milestone) string {
	name := fmt.Sprintf("%s · %s: %s", p.Title, m.Key, m.Title)
	if m.Kind == domain.MilestoneKindQA {
		name = p.Title + " · QA"
	}
	return planSessionDisplayName(name)
}

// planSessionDisplayName bounds a Task-derived session label without
// shortening the Task's stored title or the worker's prompt.
func planSessionDisplayName(name string) string {
	if utf8.RuneCountInString(name) <= displayNameMaxChars {
		return name
	}
	runes := []rune(name)
	return string(runes[:displayNameMaxChars-1]) + "…"
}

// implementerPrompt is the §6.1 initial prompt of an implementation worker.
func implementerPrompt(p domain.Plan, m domain.Milestone, deps []depInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Milestone %s: %s\n", m.Key, m.Title)
	fmt.Fprintf(&b, "Part of Task %q (plan %s). Ticket: %s\n\n", p.Title, p.ID, ticketLine(p))
	fmt.Fprintf(&b, "## Your milestone\n%s\n\n", strings.TrimSpace(m.Description))
	b.WriteString("## Dependencies already done\n")
	if len(deps) == 0 {
		b.WriteString("None.\n")
	}
	for _, d := range deps {
		if d.Branch == "" {
			fmt.Fprintf(&b, "- %s: %s — session %s has no recorded branch. Ask the orchestrator for it with `ao send` before you start.\n", d.Key, d.Title, d.SessionID)
			continue
		}
		fmt.Fprintf(&b, "- %s: %s — branch `%s` (session %s). Merge it into your branch before you start: `git merge %s`.\n", d.Key, d.Title, d.Branch, d.SessionID, d.Branch)
	}
	b.WriteString(`
## Rules for this milestone
- Build the project and run the unit and integration tests that cover your change before you report done. Fix failures yourself.
- Commit on your branch. Open the pull request as the project rules allow (the task was created by the user in the app, so publishing is authorized), and attach it with ` + "`ao report --done --pr-created <url>`" + `.
- Report progress with ` + "`ao report --checkpoint`" + `, blockers with ` + "`ao report --stuck`" + `, and completion with ` + "`ao report --done --note \"<what you verified, then what you built>\"`" + `. The plan scheduler waits for your done report. ` + doneNoteRule + `
- Do not work on other milestones. If you find the milestone cannot be completed as specified, ` + "`ao report --stuck`" + ` with the reason.
`)
	return b.String()
}

// qaPrompt is the §6.2 initial prompt of the QA worker.
func qaPrompt(p domain.Plan, built []builtInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# QA for Task %q (plan %s)\nTicket: %s\n\n## What was built\n", p.Title, p.ID, ticketLine(p))
	for _, m := range built {
		branch := "(no recorded branch; ask the orchestrator with `ao send`)"
		if m.Branch != "" {
			branch = "branch `" + m.Branch + "`"
		}
		prs := "none"
		if len(m.PRs) > 0 {
			prs = strings.Join(m.PRs, ", ")
		}
		note := clipNote(strings.TrimSpace(m.Note), PromptNoteMaxBytes)
		if note == "" {
			note = "(no summary)"
		}
		fmt.Fprintf(&b, "- %s: %s — %s, PRs: %s; summary: %s\n", m.Key, m.Title, branch, prs, note)
	}
	spec := strings.TrimSpace(p.SpecMarkdown)
	if spec == "" {
		spec = "(no plan document was stored)"
	}
	spec = truncateBytes(spec, QASpecClipBytes)
	fmt.Fprintf(&b, `
## Your job
1. Merge every milestone branch above into your branch, in the order listed (`+"`git merge <branch>`"+`). Resolve trivial conflicts; if a conflict is substantive, record a fail for the milestone that caused it.
2. Build the project, run the full test suite, and exercise the feature end to end as a user would. Use `+"`ao preview`"+` and `+"`ao browser`"+` when available; if they report AGENT_BROWSER_NOT_INSTALLED or the Browser panel is unavailable, exercise the UI with your own headless browser or HTTP checks and say which you used. Compare the behaviour against the ticket and the plan spec:

%s

3. For each defect, record it against the milestone whose change caused it:
   `+"`ao plan qa-result %[2]s --fail --milestone <key> --note \"<steps to reproduce, expected, actual, file hints>\"`"+`
   One command per milestone; put all defects for a milestone in one note.
4. If everything passes: `+"`ao plan qa-result %[2]s --pass --note \"<what you verified>\"`"+`.
5. Finish with `+"`ao report --done --note \"<summary>\"`"+`. `+doneNoteRule+` Do not open pull requests and do not push.
`, spec, p.ID)
	return b.String()
}

// defectMessage is the §6.3 delivery of QA findings to an implementation
// worker.
func defectMessage(p domain.Plan, m domain.Milestone) string {
	return fmt.Sprintf(`<ao-qa-defect plan=%q milestone=%q>
QA found problems with your milestone %q:

%s

Fix them on your current branch, rebuild, rerun the relevant tests, push the fix to your PR if one exists, then `+"`ao report --done --note \"<what you changed>\"`"+`. QA will run again after all fixes are in.
</ao-qa-defect>`, string(p.ID), m.Key, m.Title, strings.TrimSpace(m.QANote))
}

// directBrief is the composed brief a direct worker starts with: the words
// the plan request shows the orchestrator, so direct and plan workers read
// the same Task.
func directBrief(p domain.Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Task: %s\n\n%s", p.Title, strings.TrimSpace(p.Brief))
	if ref := strings.TrimSpace(p.TicketRef); ref != "" {
		fmt.Fprintf(&b, "\n\nTicket: %s", ref)
	}
	return b.String()
}

// planTriageMessage asks the orchestrator to decide how an auto Task runs. It
// carries everything the plan request does, so a plan decision continues in
// the same turn.
func planTriageMessage(p domain.Plan, projectName string) string {
	return fmt.Sprintf(`<ao-plan-triage id="%[1]s">
A new Task was created for project %[2]s, and you decide how to run it.

Title: %[3]s
Ticket: %[4]s

%[5]s

Decide now. Follow the "Plans (Tasks)" section of your standing instructions:
- One worker: `+"`ao plan triage %[1]s --direct --reason \"<one sentence>\"`"+`. AO spawns one worker with this brief; you are done until it reports.
- A plan: `+"`ao plan triage %[1]s --plan --reason \"<one sentence>\"`"+`, then plan it in this turn: investigate the repository,
  spawn research workers in parallel if the ticket is broad (attach each with `+"`ao spawn --plan %[1]s ...`"+`, and kill
  them once their findings are in the plan), write the plan with `+"`ao plan set-spec %[1]s --file <plan.md>`"+`,
  register milestones with `+"`ao plan milestone add %[1]s ...`"+`, then `+"`ao plan activate %[1]s`"+`.
</ao-plan-triage>`, p.ID, projectName, p.Title, ticketLine(p), strings.TrimSpace(p.Brief))
}

// planTriageReminderMessage reminds the orchestrator of an undecided Task.
func planTriageReminderMessage(p domain.Plan) string {
	return fmt.Sprintf(`<ao-plan-triage-reminder id="%[1]s">
Task %[2]q (plan %[1]s) has waited %[3]d minutes for your decision. Decide now with `+"`ao plan triage %[1]s --direct --reason \"...\"`"+` or `+"`ao plan triage %[1]s --plan --reason \"...\"`"+` (then plan it), or tell the human why you cannot.
</ao-plan-triage-reminder>`, p.ID, p.Title, int(TriageReminderAfter/time.Minute))
}

// planTriageFailedMessage reports a direct worker that could not start.
func planTriageFailedMessage(p domain.Plan, cause string) string {
	return fmt.Sprintf(`<ao-plan-triage-failed id="%[1]s">
The worker for Task %[2]q (plan %[1]s) could not start: %[3]s
The Task is still waiting for a decision. Run `+"`ao plan triage %[1]s --direct`"+` again once the cause is fixed, choose `+"`--plan`"+`, or `+"`ao plan cancel %[1]s`"+`.
</ao-plan-triage-failed>`, p.ID, p.Title, clipNote(strings.TrimSpace(cause), PromptNoteMaxBytes))
}

// qaOutcome is the passed QA round as the plan done notice reports it.
type qaOutcome struct {
	Note string
	// Branch is the QA worktree's branch, which merges every milestone.
	// Unresolved marks the QA session's recorded branch used as a fallback.
	Branch     string
	Unresolved bool
	SessionID  domain.SessionID
}

// planDoneMessage tells the orchestrator the plan's QA passed. The QA branch
// comes first: it is the combined result QA verified.
func planDoneMessage(p domain.Plan, built []builtInfo, qa qaOutcome) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<ao-plan-done id=%q>\nQA passed for Task %q (plan %s).\n", string(p.ID), p.Title, p.ID)
	switch {
	case qa.Branch == "":
		fmt.Fprintf(&b, "QA branch: (unresolved; ask the QA session %s which branch it merged into)\n", qa.SessionID)
	case qa.Unresolved:
		fmt.Fprintf(&b, "QA branch: %s (unresolved: the QA worktree's branch could not be read; this is the QA session's recorded branch)\n", qa.Branch)
	default:
		fmt.Fprintf(&b, "QA branch: %s (every milestone merged and verified by QA, session %s)\n", qa.Branch, qa.SessionID)
	}
	if note := clipNote(strings.TrimSpace(qa.Note), PromptNoteMaxBytes); note != "" {
		fmt.Fprintf(&b, "QA verified: %s\n", note)
	}
	b.WriteString("\nMilestones:\n")
	for _, m := range built {
		prs := "no PRs"
		if len(m.PRs) > 0 {
			prs = "PRs: " + strings.Join(m.PRs, ", ")
		}
		branch := m.Branch
		if branch == "" {
			branch = "(no recorded branch)"
		}
		fmt.Fprintf(&b, "- %s: %s — session %s, branch %s, %s\n", m.Key, m.Title, m.SessionID, branch, prs)
	}
	handoff := "the QA branch"
	if qa.Branch != "" {
		handoff += " " + qa.Branch
	}
	fmt.Fprintf(&b, "\nHand the human %s as the verified combined result, then list each milestone's branch and PR links beneath it.\n</ao-plan-done>", handoff)
	return b.String()
}

// reportRetryNote covers sandboxes that refuse the first loopback call to the
// daemon (codex workspace-write has no network): a dropped report would stall
// the plan.
const reportRetryNote = "- If an `ao` command such as `ao report` or `ao plan qa-result` fails with a connection error (for example \"operation not permitted\"), run it again, asking for permission if your sandbox offers it. The plan waits until it lands."

// planPromptSection is the system prompt addition for a plan session: which
// plan and milestone it serves and how it reports.
func planPromptSection(p domain.Plan, m domain.Milestone, role string) string {
	return planPromptSectionBody(p, m, role) + "\n" + reportRetryNote
}

func planPromptSectionBody(p domain.Plan, m domain.Milestone, role string) string {
	if role == RoleQA {
		return fmt.Sprintf(`## Plan QA
You are the QA worker for Task %q (plan %s, milestone %s). AO_PLAN_ID and AO_MILESTONE_KEY are set in your environment.
- Record every defect with `+"`ao plan qa-result %[2]s --fail --milestone <key> --note \"...\"`"+` (one command per milestone), or the overall pass with `+"`ao plan qa-result %[2]s --pass --note \"...\"`"+`.
- Then finish with `+"`ao report --done --note \"<summary>\"`"+`. A done report without a recorded verdict does not count as a pass. `+doneNoteRule+`
- Do not open pull requests and do not push.`, p.Title, p.ID, m.Key)
	}
	return fmt.Sprintf(`## Plan milestone
You implement milestone %s of Task %q (plan %s). AO_PLAN_ID and AO_MILESTONE_KEY are set in your environment.
- The plan scheduler advances only on your reports: `+"`ao report --checkpoint`"+` for progress, `+"`ao report --stuck`"+` when blocked, and `+"`ao report --done --note \"...\"`"+` when the milestone is built and its tests pass. `+doneNoteRule+`
- Commit your milestone's work on the branch you are on when you report done; AO records that branch for dependent milestones and QA. If you create a topic branch, stay on it when you run `+"`ao report --done`"+`.
- Messages wrapped in <ao-qa-defect> are QA findings for your milestone: fix them on your current branch and report done again.
- Stay within your milestone; ask the orchestrator with `+"`ao send`"+` when the plan needs changing.`, m.Key, p.Title, p.ID)
}
