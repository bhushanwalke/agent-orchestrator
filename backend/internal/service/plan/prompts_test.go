package plan

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func samplePromptPlan() domain.Plan {
	return domain.Plan{ID: "plan_1", Title: "Todo", TicketRef: "github:o/r#7", SpecMarkdown: "# Plan"}
}

func TestImplementerPromptListsEveryDependencyMerge(t *testing.T) {
	m := domain.Milestone{Key: "m3", Title: "Tests", Description: "Write the tests."}
	got := implementerPrompt(samplePromptPlan(), m, []depInfo{
		{Key: "m1", Title: "API", Branch: "ao/w-1/root", SessionID: "w-1"},
		{Key: "m2", Title: "UI", Branch: "ao/w-2/root", SessionID: "w-2"},
	})
	for _, want := range []string{
		"# Milestone m3: Tests", `Part of Task "Todo" (plan plan_1). Ticket: github:o/r#7`, "## Your milestone\nWrite the tests.",
		"- m1: API — branch `ao/w-1/root` (session w-1). Merge it into your branch before you start: `git merge ao/w-1/root`.",
		"`git merge ao/w-2/root`", "ao report --done --pr-created <url>", "The plan scheduler waits for your done report.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
	if none := implementerPrompt(samplePromptPlan(), m, nil); !strings.Contains(none, "## Dependencies already done\nNone.") {
		t.Fatalf("no-dependency prompt:\n%s", none)
	}
}

func TestQAPromptCarriesVerdictCommandsAndClipsTheSpec(t *testing.T) {
	p := samplePromptPlan()
	p.SpecMarkdown = strings.Repeat("s", QASpecClipBytes+500)
	got := qaPrompt(p, []builtInfo{{Key: "m1", Title: "API", Branch: "ao/w-1/root", PRs: []string{"https://x/pull/1"}, Note: "done"}})
	for _, want := range []string{
		`# QA for Task "Todo" (plan plan_1)`, "- m1: API — branch `ao/w-1/root`, PRs: https://x/pull/1; summary: done",
		"ao plan qa-result plan_1 --fail --milestone <key> --note", "ao plan qa-result plan_1 --pass --note",
		"Do not open pull requests and do not push.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "s") > QASpecClipBytes+200 {
		t.Fatal("spec was not clipped to 8000 bytes")
	}
}

func TestDefectMessageAndPlanSectionsTellWorkersToRetryReports(t *testing.T) {
	m := domain.Milestone{Key: "m1", Title: "API", QANote: "POST /todos returns 500"}
	defect := defectMessage(samplePromptPlan(), m)
	if !strings.HasPrefix(defect, `<ao-qa-defect plan="plan_1" milestone="m1">`) || !strings.Contains(defect, "POST /todos returns 500") ||
		!strings.HasSuffix(defect, "</ao-qa-defect>") {
		t.Fatalf("defect:\n%s", defect)
	}
	for _, role := range []string{RoleImplementer, RoleQA} {
		section := planPromptSection(samplePromptPlan(), m, role)
		if !strings.Contains(section, "connection error") || !strings.Contains(section, "run it again") {
			t.Fatalf("%s section lacks the report retry note:\n%s", role, section)
		}
	}
}

func TestClipNoteEndsAtASentenceThenAWord(t *testing.T) {
	sentence := "Built the API. " + strings.Repeat("Added routes and handlers. ", 40) + "Verified with 44 passing tests."
	got := clipNote(sentence, 100)
	if len(got) > 100 || !strings.HasSuffix(got, ". …") || strings.Contains(got, "Verified") {
		t.Fatalf("sentence clip = %q (%d bytes)", got, len(got))
	}
	words := strings.Repeat("word ", 60)
	got = clipNote(words, 50)
	if len(got) > 50 || !strings.HasSuffix(got, "word …") {
		t.Fatalf("word clip = %q", got)
	}
	if clipNote("short. note", 50) != "short. note" {
		t.Fatal("a short note must be unchanged")
	}
}

func TestPromptsAskForTheVerificationFirstInDoneNotes(t *testing.T) {
	p := samplePromptPlan()
	rule := "Start your done note with what you verified (tests run, counts), then what you built."
	impl := implementerPrompt(p, domain.Milestone{Key: "m1", Title: "API"}, nil)
	qa := qaPrompt(p, nil)
	section := planPromptSection(p, domain.Milestone{Key: "m1"}, RoleImplementer)
	for name, text := range map[string]string{"implementer": impl, "qa": qa, "section": section} {
		if !strings.Contains(text, rule) {
			t.Fatalf("%s prompt lacks the done-note rule:\n%s", name, text)
		}
	}
	long := "Verified: npm test passes 44/44. " + strings.Repeat("Implemented a thing. ", 60)
	if got := qaPrompt(p, []builtInfo{{Key: "m1", Title: "API", Note: long}}); !strings.Contains(got, "Verified: npm test passes 44/44.") {
		t.Fatal("qa prompt dropped the verification")
	}
	if PromptNoteMaxBytes != 900 {
		t.Fatalf("PromptNoteMaxBytes = %d", PromptNoteMaxBytes)
	}
}

func TestQAPromptFallsBackWhenTheBrowserIsUnavailable(t *testing.T) {
	want := "Use `ao preview` and `ao browser` when available; if they report AGENT_BROWSER_NOT_INSTALLED or the Browser panel is unavailable, exercise the UI with your own headless browser or HTTP checks and say which you used."
	if got := qaPrompt(samplePromptPlan(), nil); !strings.Contains(got, want) {
		t.Fatalf("qa prompt lacks the browser fallback:\n%s", got)
	}
}
