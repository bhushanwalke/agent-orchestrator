package plan

import (
	"fmt"
	"strings"
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
- Report progress with ` + "`ao report --checkpoint`" + `, blockers with ` + "`ao report --stuck`" + `, and completion with ` + "`ao report --done --note \"<what you built, how you verified it>\"`" + `. The plan scheduler waits for your done report.
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
		note := strings.TrimSpace(m.Note)
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
2. Build the project, run the full test suite, and exercise the feature end to end as a user would (use `+"`ao preview`"+` and `+"`ao browser`"+` for web apps). Compare the behaviour against the ticket and the plan spec:

%s

3. For each defect, record it against the milestone whose change caused it:
   `+"`ao plan qa-result %[2]s --fail --milestone <key> --note \"<steps to reproduce, expected, actual, file hints>\"`"+`
   One command per milestone; put all defects for a milestone in one note.
4. If everything passes: `+"`ao plan qa-result %[2]s --pass --note \"<what you verified>\"`"+`.
5. Finish with `+"`ao report --done --note \"<summary>\"`"+`. Do not open pull requests and do not push.
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

// planDoneMessage tells the orchestrator the plan's QA passed.
func planDoneMessage(p domain.Plan, built []builtInfo, qaNote string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<ao-plan-done id=%q>\nQA passed for Task %q (plan %s).\n", string(p.ID), p.Title, p.ID)
	if note := strings.TrimSpace(qaNote); note != "" {
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
	b.WriteString("\nSummarise the outcome and the PR links for the human.\n</ao-plan-done>")
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
- Then finish with `+"`ao report --done --note \"<summary>\"`"+`. A done report without a recorded verdict does not count as a pass.
- Do not open pull requests and do not push.`, p.Title, p.ID, m.Key)
	}
	return fmt.Sprintf(`## Plan milestone
You implement milestone %s of Task %q (plan %s). AO_PLAN_ID and AO_MILESTONE_KEY are set in your environment.
- The plan scheduler advances only on your reports: `+"`ao report --checkpoint`"+` for progress, `+"`ao report --stuck`"+` when blocked, and `+"`ao report --done --note \"...\"`"+` when the milestone is built and its tests pass.
- Commit your milestone's work on the branch you are on when you report done; AO records that branch for dependent milestones and QA. If you create a topic branch, stay on it when you run `+"`ao report --done`"+`.
- Messages wrapped in <ao-qa-defect> are QA findings for your milestone: fix them on your current branch and report done again.
- Stay within your milestone; ask the orchestrator with `+"`ao send`"+` when the plan needs changing.`, m.Key, p.Title, p.ID)
}
