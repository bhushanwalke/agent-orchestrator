package domain

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

func TestValidateMilestoneKey(t *testing.T) {
	for _, key := range []string{"m1", "a", "0", "api-v2", strings.Repeat("a", 32)} {
		if err := ValidateMilestoneKey(key); err != nil {
			t.Errorf("key %q rejected: %v", key, err)
		}
	}
	for _, key := range []string{"", "-m1", "M1", "m_1", "m 1", strings.Repeat("a", 33), "m1/x"} {
		if err := ValidateMilestoneKey(key); err == nil {
			t.Errorf("key %q accepted", key)
		}
	}
}

func implMs(key string, deps ...string) Milestone {
	return Milestone{Key: key, Kind: MilestoneKindImplement, Title: "T " + key, Description: "do " + key, DependsOn: deps}
}

func qaMs() Milestone {
	return Milestone{Key: QAMilestoneKey, Kind: MilestoneKindQA, Title: "QA"}
}

func TestMilestoneValidate(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(*Milestone)
		field string
	}{
		{"ok", func(*Milestone) {}, ""},
		{"bad key", func(m *Milestone) { m.Key = "Bad" }, "key"},
		{"reserved qa key on implement", func(m *Milestone) { m.Key = "qa" }, "key"},
		{"qa kind needs qa key", func(m *Milestone) { m.Kind = MilestoneKindQA; m.Description = "" }, "key"},
		{"unknown kind", func(m *Milestone) { m.Kind = "deploy" }, "kind"},
		{"empty title", func(m *Milestone) { m.Title = "   " }, "title"},
		{"title at limit", func(m *Milestone) { m.Title = strings.Repeat("é", 100) }, ""},
		{"long title", func(m *Milestone) { m.Title = strings.Repeat("é", 101) }, "title"},
		{"implement needs description", func(m *Milestone) { m.Description = " " }, "description"},
		{"description too long", func(m *Milestone) { m.Description = strings.Repeat("x", MilestoneDescriptionMaxBytes+1) }, "description"},
		{"absolute sparse", func(m *Milestone) { m.SparsePaths = []string{"/etc"} }, "sparsePaths"},
		{"escaping sparse", func(m *Milestone) { m.SparsePaths = []string{"a/../../b"} }, "sparsePaths"},
		{"empty sparse", func(m *Milestone) { m.SparsePaths = []string{" "} }, "sparsePaths"},
		{"too many sparse", func(m *Milestone) { m.SparsePaths = make([]string, 51); fillPaths(m.SparsePaths) }, "sparsePaths"},
		{"self dependency", func(m *Milestone) { m.DependsOn = []string{"m1"} }, "dependsOn"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := implMs("m1")
			tc.edit(&m)
			err := m.Validate()
			if tc.field == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			var fe *MilestoneFieldError
			if !errors.As(err, &fe) || fe.Field != tc.field {
				t.Fatalf("err = %v, want field %q", err, tc.field)
			}
		})
	}
	qa := qaMs()
	qa.SparsePaths = []string{"src"}
	var fe *MilestoneFieldError
	if err := qa.Validate(); !errors.As(err, &fe) || fe.Field != "sparsePaths" {
		t.Fatalf("qa with sparse paths err = %v", err)
	}
	if err := qaMs().Validate(); err != nil {
		t.Fatalf("plain qa milestone: %v", err)
	}
}

func fillPaths(p []string) {
	for i := range p {
		p[i] = "d" + strings.Repeat("x", i)
	}
}

func TestValidatePlanGraph(t *testing.T) {
	cases := []struct {
		name    string
		ms      []Milestone
		wantSub []string
	}{
		{"ok chain", []Milestone{implMs("m1"), implMs("m2", "m1"), implMs("m3", "m1", "m2"), qaMs()}, nil},
		{"cycle", []Milestone{implMs("m1", "m2"), implMs("m2", "m1")}, []string{"cycle", "m1", "m2"}},
		{"long cycle", []Milestone{implMs("m1", "m3"), implMs("m2", "m1"), implMs("m3", "m2"), implMs("m4")}, []string{"cycle", "m1", "m2", "m3"}},
		{"unknown dep", []Milestone{implMs("m1", "zz")}, []string{"m1", "zz"}},
		{"qa depended on", []Milestone{implMs("m1", "qa"), qaMs()}, []string{"m1", "qa"}},
		{"duplicate key", []Milestone{implMs("m1"), implMs("m1")}, []string{"m1"}},
		{"two qa", []Milestone{implMs("m1"), qaMs(), qaMs()}, []string{"qa"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePlanGraph(tc.ms)
			if tc.wantSub == nil {
				if err != nil {
					t.Fatalf("unexpected: %v", err)
				}
				return
			}
			var ge *PlanGraphError
			if !errors.As(err, &ge) {
				t.Fatalf("err = %v, want PlanGraphError", err)
			}
			for _, sub := range tc.wantSub {
				if !strings.Contains(err.Error(), sub) {
					t.Fatalf("err %q missing %q", err, sub)
				}
			}
		})
	}
	many := make([]Milestone, MaxPlanMilestones+1)
	for i := range many {
		many[i] = implMs("m" + strings.Repeat("1", i+1))
	}
	if err := ValidatePlanGraph(many); err == nil {
		t.Fatal("21 milestones accepted")
	}
}

func TestValidatePlanActivation(t *testing.T) {
	if err := ValidatePlanActivation([]Milestone{qaMs()}); err == nil || !strings.Contains(err.Error(), "implement") {
		t.Fatalf("qa only err = %v", err)
	}
	if err := ValidatePlanActivation([]Milestone{implMs("m1")}); err == nil || !strings.Contains(err.Error(), "qa") {
		t.Fatalf("missing qa err = %v", err)
	}
	if err := ValidatePlanActivation([]Milestone{implMs("m1"), qaMs()}); err != nil {
		t.Fatalf("valid activation: %v", err)
	}
}

func TestTopoOrder(t *testing.T) {
	ms := []Milestone{implMs("m3", "m1", "m2"), implMs("m2"), implMs("m1"), qaMs()}
	for i := range ms {
		ms[i].Ordinal = i
	}
	got, err := TopoOrder(ms)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, m := range got {
		keys = append(keys, m.Key)
	}
	if strings.Join(keys, ",") != "m2,m1,m3,qa" {
		t.Fatalf("order = %v", keys)
	}
	if _, err := TopoOrder([]Milestone{implMs("a", "b"), implMs("b", "a")}); err == nil {
		t.Fatal("cycle accepted")
	}
}

func ms(key string, kind MilestoneKind, status MilestoneStatus) Milestone {
	return Milestone{Key: key, Kind: kind, Status: status}
}

func TestDerivePlanPhase(t *testing.T) {
	active := Plan{State: PlanStateActive}
	impl := MilestoneKindImplement
	qa := MilestoneKindQA
	withVerdict := func(m Milestone, v QAVerdict) Milestone { m.QAVerdict = v; return m }
	withAttempt := func(m Milestone, a int) Milestone { m.Attempt = a; return m }
	created := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	needsYouStage := map[string]contract.KanbanColumn{
		"triage after the reminder":  contract.KanbanBuilding,
		"triage on hold":             contract.KanbanBuilding,
		"planning on hold":           contract.KanbanBuilding,
		"implementing on hold":       contract.KanbanBuilding,
		"planning paused for you":    contract.KanbanBuilding,
		"testing paused for you":     contract.KanbanValidating,
		"blocked while implementing": contract.KanbanBuilding,
		"blocked qa":                 contract.KanbanBuilding,
		"failed after retries":       contract.KanbanNeedsReview,
	}
	cases := []struct {
		name    string
		plan    Plan
		ms      []Milestone
		phase   PlanPhase
		column  contract.KanbanColumn
		display contract.DisplayStatus
	}{
		{"cancelled", Plan{State: PlanStateCancelled}, []Milestone{ms("m1", impl, MilestoneStatusCancelled)},
			PlanPhaseCancelled, contract.KanbanArchive, contract.DisplayPlanCancelled},
		{"planning", Plan{State: PlanStatePlanning}, nil,
			PlanPhasePlanning, contract.KanbanBuilding, contract.DisplayPlanning},
		{"triage before the reminder", Plan{State: PlanStateTriage, IntakeMode: PlanIntakeAuto, CreatedAt: created}, nil,
			PlanPhaseTriage, contract.KanbanQueued, contract.DisplayDeciding},
		{"triage after the reminder", Plan{State: PlanStateTriage, IntakeMode: PlanIntakeAuto, CreatedAt: created.Add(-TriageReminderAfter)}, nil,
			PlanPhaseTriage, contract.KanbanNeedsYou, contract.DisplayNeedsADecision},
		{"triage on hold", Plan{State: PlanStateTriage, IntakeMode: PlanIntakeAuto, CreatedAt: created.Add(-time.Hour), BoardRequest: BoardRequestHold}, nil,
			PlanPhaseTriage, contract.KanbanQueued, contract.DisplayOnHold},
		{"planning on hold", Plan{State: PlanStatePlanning, BoardRequest: BoardRequestHold}, nil,
			PlanPhasePlanning, contract.KanbanQueued, contract.DisplayOnHold},
		{"implementing on hold", Plan{State: PlanStateActive, BoardRequest: BoardRequestHold}, []Milestone{ms("m1", impl, MilestoneStatusReady)},
			PlanPhaseImplementing, contract.KanbanQueued, contract.DisplayOnHold},
		{"planning paused for you", Plan{State: PlanStatePlanning, BoardRequest: BoardRequestAttention}, nil,
			PlanPhasePlanning, contract.KanbanNeedsYou, contract.DisplayPausedForYou},
		{"testing paused for you", Plan{State: PlanStateActive, BoardRequest: BoardRequestAttention}, []Milestone{ms("m1", impl, MilestoneStatusDone), ms("qa", qa, MilestoneStatusRunning)},
			PlanPhaseQA, contract.KanbanNeedsYou, contract.DisplayPausedForYou},
		{"cancelled ignores a request", Plan{State: PlanStateCancelled, BoardRequest: BoardRequestHold}, nil,
			PlanPhaseCancelled, contract.KanbanArchive, contract.DisplayPlanCancelled},
		{"direct", Plan{State: PlanStateDirect, IntakeMode: PlanIntakeAuto, DirectSessionID: "w-1"}, nil,
			PlanPhaseDirect, contract.KanbanArchive, contract.DisplayOneWorker},
		{"blocked while implementing", active, []Milestone{ms("m1", impl, MilestoneStatusBlocked), ms("m2", impl, MilestoneStatusDone), ms("qa", qa, MilestoneStatusPending)},
			PlanPhaseImplementing, contract.KanbanNeedsYou, contract.DisplayBlocked},
		{"qa failed", active, []Milestone{ms("m1", impl, MilestoneStatusDone), ms("qa", qa, MilestoneStatusFailed)},
			PlanPhaseFixing, contract.KanbanBuilding, contract.DisplayFixingQAFindings},
		{"qa defect being fixed", active, []Milestone{withVerdict(ms("m1", impl, MilestoneStatusRunning), QAVerdictFail), ms("qa", qa, MilestoneStatusPending)},
			PlanPhaseFixing, contract.KanbanBuilding, contract.DisplayFixingQAFindings},
		{"qa running", active, []Milestone{ms("m1", impl, MilestoneStatusDone), ms("qa", qa, MilestoneStatusRunning)},
			PlanPhaseQA, contract.KanbanValidating, contract.DisplayTesting},
		{"blocked qa", active, []Milestone{ms("m1", impl, MilestoneStatusDone), ms("qa", qa, MilestoneStatusBlocked)},
			PlanPhaseQA, contract.KanbanNeedsYou, contract.DisplayBlocked},
		{"qa passed", active, []Milestone{ms("m1", impl, MilestoneStatusDone), withVerdict(ms("qa", qa, MilestoneStatusDone), QAVerdictPass)},
			PlanPhaseDone, contract.KanbanReady, contract.DisplayQAPassed},
		{"failed after retries", active, []Milestone{withAttempt(ms("m1", impl, MilestoneStatusFailed), 3), ms("qa", qa, MilestoneStatusPending)},
			PlanPhaseFailed, contract.KanbanNeedsYou, contract.DisplayNeedsYou},
		{"failed but retrying", active, []Milestone{withAttempt(ms("m1", impl, MilestoneStatusFailed), 1), ms("m2", impl, MilestoneStatusDone), ms("qa", qa, MilestoneStatusPending)},
			PlanPhaseImplementing, contract.KanbanBuilding, contract.DisplayStatus("Implementing 1/2")},
		{"implementing 2/5", active, []Milestone{
			ms("m1", impl, MilestoneStatusDone), ms("m2", impl, MilestoneStatusDone), ms("m3", impl, MilestoneStatusRunning),
			ms("m4", impl, MilestoneStatusPending), ms("m5", impl, MilestoneStatusReady), ms("qa", qa, MilestoneStatusPending)},
			PlanPhaseImplementing, contract.KanbanBuilding, contract.DisplayStatus("Implementing 2/5")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			phase := DerivePlanPhase(tc.plan, tc.ms)
			if phase != tc.phase {
				t.Fatalf("phase = %q, want %q", phase, tc.phase)
			}
			col, display, stage := DerivePlanKanban(tc.plan, phase, tc.ms, created.Add(time.Minute))
			if col != tc.column || display != tc.display {
				t.Fatalf("kanban = %q/%q, want %q/%q", col, display, tc.column, tc.display)
			}
			wantStage := tc.column
			if s, ok := needsYouStage[tc.name]; ok {
				wantStage = s
			}
			if stage != wantStage {
				t.Fatalf("stage = %q, want %q", stage, wantStage)
			}
		})
	}
}

func TestPlanPhaseFinishedAndTaskCard(t *testing.T) {
	for _, phase := range []PlanPhase{PlanPhaseTriage, PlanPhasePlanning, PlanPhaseImplementing, PlanPhaseQA, PlanPhaseFixing, PlanPhaseFailed} {
		if phase.Finished() {
			t.Errorf("%s reads as finished", phase)
		}
		if !phase.ShowsTaskCard() {
			t.Errorf("%s hides its Task card", phase)
		}
	}
	for _, phase := range []PlanPhase{PlanPhaseDone, PlanPhaseCancelled, PlanPhaseDirect} {
		if !phase.Finished() {
			t.Errorf("%s does not read as finished", phase)
		}
	}
	if PlanPhaseDirect.ShowsTaskCard() {
		t.Error("a direct Task shows a Task card; its worker card stands for it")
	}
}

func TestTriageDecisionAndActorValid(t *testing.T) {
	if !TriageDecisionPlan.Valid() || !TriageDecisionDirect.Valid() || TriageDecision("").Valid() || TriageDecision("maybe").Valid() {
		t.Error("TriageDecision.Valid accepts the wrong set")
	}
	if !TriageActorOrchestrator.Valid() || !TriageActorHuman.Valid() || TriageActor("").Valid() {
		t.Error("TriageActor.Valid accepts the wrong set")
	}
}
