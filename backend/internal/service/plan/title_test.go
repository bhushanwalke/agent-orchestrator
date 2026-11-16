package plan_test

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/plan"
)

const oceanTaskTitle = "OCEAN-20601: Increase OBI AI coverage — raise the share of OBI shipments that clear HOTW/AR automation"

func TestTaskTitleCreateAndUpdate(t *testing.T) {
	for _, tc := range []struct {
		name, title string
	}{
		{"empty", ""},
		{"whitespace", " \t\r\n"},
		{"unicode whitespace", "\u00a0\u2003\u3000"},
		{"one character", "a"},
		{"100 characters", strings.Repeat("a", 100)},
		{"101 characters", strings.Repeat("a", 101)},
		{"original Ocean title", oceanTaskTitle},
		{"long title", strings.Repeat("a", 4096)},
		{"100 multibyte characters", strings.Repeat("界", 100)},
		{"101 multibyte characters", strings.Repeat("界", 101)},
		{"emoji and combining marks", strings.Repeat("🛳️ e\u0301 ", 30)},
		{"trimmed title", "\u00a0\t" + oceanTaskTitle + "\u2003\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, operation := range []string{"plan", "auto", "update"} {
				t.Run(operation, func(t *testing.T) {
					f := newFixture(t, domain.ProjectKindSingleRepo)
					ctx := context.Background()
					var got domain.Plan
					var err error
					if operation == "update" {
						p := createPlan(t, f)
						summary, updateErr := f.svc.Update(ctx, p.ID, plan.Patch{Title: &tc.title})
						got, err = summary.Plan, updateErr
						if strings.TrimSpace(tc.title) == "" {
							stored, lookupErr := f.svc.Lookup(ctx, p.ID)
							if lookupErr != nil || stored.Title != p.Title {
								t.Fatalf("invalid update changed title: %q, err=%v", stored.Title, lookupErr)
							}
						}
					} else {
						got, err = f.svc.Create(ctx, plan.CreateInput{
							ProjectID: "mer", Title: tc.title, Brief: "Build it", Mode: domain.PlanIntakeMode(operation),
						})
					}
					want := strings.TrimSpace(tc.title)
					if want == "" {
						ae := wantCode(t, err, "INVALID_PLAN")
						if ae.Message != "Title is required" || ae.Details["field"] != "title" {
							t.Fatalf("title error = %+v", ae)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if got.Title != want {
						t.Fatalf("title = %q, want %q", got.Title, want)
					}
					detail, err := f.svc.Get(ctx, got.ID)
					if err != nil || detail.Plan.Title != want {
						t.Fatalf("persisted title = %q, err=%v, want %q", detail.Plan.Title, err, want)
					}
					if operation != "update" && !strings.Contains(f.sessions.sent[0].Text, want) {
						t.Fatal("kickoff omitted the full Task title")
					}
				})
			}
		})
	}
}

func TestLongAutoTaskTitleKeepsWorkerNameBounded(t *testing.T) {
	for _, title := range []string{oceanTaskTitle, strings.Repeat("界🛳", 60)} {
		f, _ := newTriageFixture(t)
		ctx := context.Background()
		p, err := f.svc.Create(ctx, plan.CreateInput{ProjectID: "mer", Title: title, Brief: "Build it", Mode: domain.PlanIntakeAuto})
		if err != nil {
			t.Fatal(err)
		}
		got, err := f.svc.Triage(ctx, p.ID, domain.TriageDecisionDirect, "one worker", domain.TriageActorHuman)
		if err != nil {
			t.Fatal(err)
		}
		cfg := f.sessions.spawns[0]
		wantName := string([]rune(title)[:99]) + "…"
		if cfg.DisplayName != wantName || !utf8.ValidString(cfg.DisplayName) {
			t.Fatalf("worker name = %q, want %q", cfg.DisplayName, wantName)
		}
		stored, err := f.svc.Lookup(ctx, p.ID)
		if err != nil || stored.Title != title || got.Title != title || !strings.Contains(cfg.Prompt, title) {
			t.Fatalf("full Task title lost: stored=%q returned=%q prompt=%q err=%v", stored.Title, got.Title, cfg.Prompt, err)
		}
	}
}
