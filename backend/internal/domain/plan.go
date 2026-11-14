package domain

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

// PlanID identifies a task plan ("plan_" + uuid).
type PlanID string

// MilestoneID identifies one milestone of a plan ("ms_" + uuid).
type MilestoneID string

// PlanState is the only persisted plan lifecycle fact. Everything a reader
// shows beyond it is derived from the milestones (see DerivePlanPhase).
type PlanState string

// Plan states.
const (
	PlanStatePlanning  PlanState = "planning"
	PlanStateActive    PlanState = "active"
	PlanStateCancelled PlanState = "cancelled"
)

// MilestoneKind distinguishes implementation milestones from the QA milestone.
type MilestoneKind string

// Milestone kinds.
const (
	MilestoneKindImplement MilestoneKind = "implement"
	MilestoneKindQA        MilestoneKind = "qa"
)

// MilestoneStatus is a milestone's scheduling state.
type MilestoneStatus string

// Milestone statuses.
const (
	MilestoneStatusPending   MilestoneStatus = "pending"
	MilestoneStatusReady     MilestoneStatus = "ready"
	MilestoneStatusRunning   MilestoneStatus = "running"
	MilestoneStatusBlocked   MilestoneStatus = "blocked"
	MilestoneStatusDone      MilestoneStatus = "done"
	MilestoneStatusFailed    MilestoneStatus = "failed"
	MilestoneStatusCancelled MilestoneStatus = "cancelled"
)

// QAVerdict is the QA worker's verdict recorded on a milestone.
type QAVerdict string

// QA verdicts. The empty verdict means none was recorded.
const (
	QAVerdictNone QAVerdict = ""
	QAVerdictPass QAVerdict = "pass"
	QAVerdictFail QAVerdict = "fail"
)

// PlanPhase is the derived, never persisted, phase of a plan.
type PlanPhase string

// Plan phases.
const (
	PlanPhasePlanning     PlanPhase = "planning"
	PlanPhaseImplementing PlanPhase = "implementing"
	PlanPhaseQA           PlanPhase = "qa"
	PlanPhaseFixing       PlanPhase = "fixing"
	PlanPhaseDone         PlanPhase = "done"
	PlanPhaseCancelled    PlanPhase = "cancelled"
	PlanPhaseFailed       PlanPhase = "failed"
)

// Plan limits.
const (
	MaxPlanMilestones            = 20
	DefaultPlanMaxParallel       = 3
	MinPlanMaxParallel           = 1
	MaxPlanMaxParallel           = 10
	PlanTitleMaxChars            = 100
	PlanBriefMaxBytes            = 102400
	MilestoneDescriptionMaxBytes = 32768
	QANoteMaxBytes               = 8192
	MaxMilestoneSparsePaths      = 50
	// MaxMilestoneAttempts bounds automatic spawns of one milestone before
	// the plan needs a person.
	MaxMilestoneAttempts = 3
	// QAMilestoneKey is reserved for the plan's single QA milestone.
	QAMilestoneKey = "qa"
)

// Plan is one task: a ticket the orchestrator plans into milestones.
type Plan struct {
	ID                    PlanID
	ProjectID             ProjectID
	Title                 string
	Brief                 string
	TicketRef             string
	SpecMarkdown          string
	State                 PlanState
	OrchestratorSessionID SessionID
	MaxParallel           int
	CreatedAt             time.Time
	UpdatedAt             time.Time
	ActivatedAt           *time.Time
	FinishedAt            *time.Time
}

// Milestone is one unit of a plan, owned by at most one session at a time.
type Milestone struct {
	ID          MilestoneID
	PlanID      PlanID
	Key         string
	Ordinal     int
	Kind        MilestoneKind
	Title       string
	Description string
	Status      MilestoneStatus
	SessionID   SessionID
	SparsePaths []string
	// DependsOn lists the keys of milestones in the same plan this one needs.
	DependsOn  []string
	QAVerdict  QAVerdict
	QANote     string
	Attempt    int
	CreatedAt  time.Time
	UpdatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}

// PlanEvent is one entry in a plan's activity log.
type PlanEvent struct {
	ID          int64
	PlanID      PlanID
	MilestoneID MilestoneID
	SessionID   SessionID
	Kind        string
	Note        string
	CreatedAt   time.Time
}

// ErrMilestoneKeyExists reports a milestone key already used in its plan.
var ErrMilestoneKeyExists = errors.New("milestone key already exists in this plan")

var milestoneKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// ValidateMilestoneKey checks a milestone key's shape: a lowercase slug of at
// most 32 characters starting with a letter or digit.
func ValidateMilestoneKey(key string) error {
	if !milestoneKeyPattern.MatchString(key) {
		return fmt.Errorf("milestone key %q must match %s", key, milestoneKeyPattern.String())
	}
	return nil
}

// MilestoneFieldError reports one invalid milestone field. Field uses the API
// field name so callers can surface it as details.field.
type MilestoneFieldError struct {
	Field   string
	Message string
}

func (e *MilestoneFieldError) Error() string { return e.Field + ": " + e.Message }

func milestoneFieldError(field, format string, args ...any) error {
	return &MilestoneFieldError{Field: field, Message: fmt.Sprintf(format, args...)}
}

// Validate checks one milestone on its own. Graph-level rules (unknown or
// cyclic dependencies, duplicate keys) are ValidatePlanGraph's.
func (m Milestone) Validate() error {
	switch m.Kind {
	case MilestoneKindImplement, MilestoneKindQA:
	default:
		return milestoneFieldError("kind", "kind must be implement or qa")
	}
	if err := ValidateMilestoneKey(m.Key); err != nil {
		return milestoneFieldError("key", "%s", err.Error())
	}
	if (m.Key == QAMilestoneKey) != (m.Kind == MilestoneKindQA) {
		return milestoneFieldError("key", "the key %q is reserved for the qa milestone, which must use it", QAMilestoneKey)
	}
	title := strings.TrimSpace(m.Title)
	if title == "" || utf8.RuneCountInString(title) > PlanTitleMaxChars {
		return milestoneFieldError("title", "title must be 1-%d characters", PlanTitleMaxChars)
	}
	if m.Kind == MilestoneKindImplement && strings.TrimSpace(m.Description) == "" {
		return milestoneFieldError("description", "an implement milestone needs a description")
	}
	if len(m.Description) > MilestoneDescriptionMaxBytes {
		return milestoneFieldError("description", "description must be at most %d bytes", MilestoneDescriptionMaxBytes)
	}
	if m.Kind == MilestoneKindQA && len(m.SparsePaths) > 0 {
		return milestoneFieldError("sparsePaths", "the qa milestone always gets a full checkout")
	}
	if len(m.SparsePaths) > MaxMilestoneSparsePaths {
		return milestoneFieldError("sparsePaths", "at most %d sparse paths", MaxMilestoneSparsePaths)
	}
	for _, p := range m.SparsePaths {
		if strings.TrimSpace(p) == "" {
			return milestoneFieldError("sparsePaths", "sparse paths must not be empty")
		}
		if err := validateRepoRelative(p); err != nil {
			return milestoneFieldError("sparsePaths", "%q: %s", p, err.Error())
		}
	}
	if m.Kind == MilestoneKindQA && len(m.DependsOn) > 0 {
		return milestoneFieldError("dependsOn", "the qa milestone depends on every implement milestone; it takes no explicit dependencies")
	}
	for _, dep := range m.DependsOn {
		if dep == m.Key {
			return milestoneFieldError("dependsOn", "a milestone cannot depend on itself")
		}
	}
	return nil
}

// WithDerivedQADeps returns ms with the qa milestone's DependsOn set to every
// implement milestone's key in ordinal order. The qa dependencies are implicit
// and never stored; read paths call this so they are reported consistently.
func WithDerivedQADeps(ms []Milestone) []Milestone {
	out := make([]Milestone, len(ms))
	copy(out, ms)
	var keys []string
	for _, m := range sortedByOrdinal(ms) {
		if m.Kind == MilestoneKindImplement {
			keys = append(keys, m.Key)
		}
	}
	for i := range out {
		if out[i].Kind == MilestoneKindQA {
			out[i].DependsOn = append([]string{}, keys...)
		}
	}
	return out
}

func sortedByOrdinal(ms []Milestone) []Milestone {
	sorted := append([]Milestone(nil), ms...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Ordinal < sorted[j].Ordinal })
	return sorted
}

// PlanGraphError reports why a plan's milestone graph cannot run.
type PlanGraphError struct {
	Problem string
	Keys    []string
}

func (e *PlanGraphError) Error() string { return e.Problem }

// ValidatePlanGraph checks the milestones of one plan together: each is valid,
// keys are unique, at most one qa milestone exists, every dependency names a
// milestone of the plan other than qa, and the graph is acyclic.
func ValidatePlanGraph(ms []Milestone) error {
	if len(ms) > MaxPlanMilestones {
		return &PlanGraphError{Problem: fmt.Sprintf("a plan has at most %d milestones", MaxPlanMilestones)}
	}
	byKey := make(map[string]Milestone, len(ms))
	qaCount := 0
	for _, m := range ms {
		if err := m.Validate(); err != nil {
			return &PlanGraphError{Problem: fmt.Sprintf("milestone %s: %s", m.Key, err.Error()), Keys: []string{m.Key}}
		}
		if _, dup := byKey[m.Key]; dup {
			if m.Kind == MilestoneKindQA {
				return &PlanGraphError{Problem: "a plan has exactly one qa milestone", Keys: []string{m.Key}}
			}
			return &PlanGraphError{Problem: fmt.Sprintf("milestone key %s is used twice", m.Key), Keys: []string{m.Key}}
		}
		byKey[m.Key] = m
		if m.Kind == MilestoneKindQA {
			qaCount++
		}
	}
	if qaCount > 1 {
		return &PlanGraphError{Problem: "a plan has exactly one qa milestone", Keys: []string{QAMilestoneKey}}
	}
	for _, m := range ms {
		for _, dep := range m.DependsOn {
			target, ok := byKey[dep]
			if !ok {
				return &PlanGraphError{Problem: fmt.Sprintf("milestone %s depends on unknown milestone %s", m.Key, dep), Keys: []string{m.Key, dep}}
			}
			if target.Kind == MilestoneKindQA {
				return &PlanGraphError{Problem: fmt.Sprintf("milestone %s cannot depend on the qa milestone %s", m.Key, dep), Keys: []string{m.Key, dep}}
			}
		}
	}
	if cycle := findCycle(ms); cycle != nil {
		return &PlanGraphError{Problem: "dependency cycle: " + strings.Join(cycle, " -> "), Keys: cycle[:len(cycle)-1]}
	}
	return nil
}

// ValidatePlanActivation adds the activation-time rules to ValidatePlanGraph:
// at least one implement milestone and exactly one qa milestone.
func ValidatePlanActivation(ms []Milestone) error {
	if err := ValidatePlanGraph(ms); err != nil {
		return err
	}
	implement, qa := 0, 0
	for _, m := range ms {
		switch m.Kind {
		case MilestoneKindImplement:
			implement++
		case MilestoneKindQA:
			qa++
		}
	}
	if implement == 0 {
		return &PlanGraphError{Problem: "a plan needs at least one implement milestone"}
	}
	if qa != 1 {
		return &PlanGraphError{Problem: "a plan needs exactly one qa milestone"}
	}
	return nil
}

// findCycle returns one dependency cycle as keys with the first key repeated
// at the end, or nil when the graph is acyclic. Unknown keys are ignored.
func findCycle(ms []Milestone) []string {
	deps := make(map[string][]string, len(ms))
	for _, m := range ms {
		deps[m.Key] = m.DependsOn
	}
	const (
		unvisited = iota
		visiting
		visited
	)
	state := make(map[string]int, len(ms))
	var stack []string
	var walk func(string) []string
	walk = func(key string) []string {
		state[key] = visiting
		stack = append(stack, key)
		for _, dep := range deps[key] {
			if _, known := deps[dep]; !known {
				continue
			}
			switch state[dep] {
			case visiting:
				for i, k := range stack {
					if k == dep {
						return append(append([]string{}, stack[i:]...), dep)
					}
				}
			case unvisited:
				if c := walk(dep); c != nil {
					return c
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[key] = visited
		return nil
	}
	for _, m := range ms {
		if state[m.Key] == unvisited {
			if c := walk(m.Key); c != nil {
				return c
			}
		}
	}
	return nil
}

// TopoOrder returns the milestones in dependency order (Kahn), breaking ties
// by ordinal. The qa milestone implicitly follows every implement milestone.
func TopoOrder(ms []Milestone) ([]Milestone, error) {
	sorted := append([]Milestone(nil), ms...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Ordinal < sorted[j].Ordinal })
	indegree := make(map[string]int, len(sorted))
	dependents := make(map[string][]string, len(sorted))
	known := make(map[string]bool, len(sorted))
	for _, m := range sorted {
		known[m.Key] = true
	}
	for _, m := range sorted {
		deps := effectiveDeps(m, sorted)
		for _, dep := range deps {
			if !known[dep] {
				continue
			}
			indegree[m.Key]++
			dependents[dep] = append(dependents[dep], m.Key)
		}
	}
	out := make([]Milestone, 0, len(sorted))
	done := make(map[string]bool, len(sorted))
	for len(out) < len(sorted) {
		progressed := false
		for _, m := range sorted {
			if done[m.Key] || indegree[m.Key] > 0 {
				continue
			}
			done[m.Key] = true
			out = append(out, m)
			for _, d := range dependents[m.Key] {
				indegree[d]--
			}
			progressed = true
			break
		}
		if !progressed {
			return nil, &PlanGraphError{Problem: "dependency cycle"}
		}
	}
	return out, nil
}

// effectiveDeps is a milestone's dependency keys, with the qa milestone
// depending on every implement milestone.
func effectiveDeps(m Milestone, all []Milestone) []string {
	if m.Kind != MilestoneKindQA {
		return m.DependsOn
	}
	var keys []string
	for _, other := range all {
		if other.Kind == MilestoneKindImplement {
			keys = append(keys, other.Key)
		}
	}
	return keys
}

// DerivePlanPhase derives the phase a plan reads as from its persisted state
// and its milestones. A blocked milestone does not change the phase; it only
// changes the display status (DerivePlanKanban).
func DerivePlanPhase(plan Plan, ms []Milestone) PlanPhase {
	switch plan.State {
	case PlanStateCancelled:
		return PlanPhaseCancelled
	case PlanStatePlanning:
		return PlanPhasePlanning
	}
	var qa *Milestone
	for i := range ms {
		if ms[i].Kind == MilestoneKindQA {
			qa = &ms[i]
		}
	}
	for _, m := range ms {
		if m.Kind == MilestoneKindImplement && m.QAVerdict == QAVerdictFail &&
			(m.Status == MilestoneStatusReady || m.Status == MilestoneStatusRunning || m.Status == MilestoneStatusBlocked) {
			return PlanPhaseFixing
		}
	}
	if qa != nil {
		switch {
		case qa.Status == MilestoneStatusFailed:
			return PlanPhaseFixing
		case qa.Status == MilestoneStatusRunning || qa.Status == MilestoneStatusBlocked:
			return PlanPhaseQA
		case qa.Status == MilestoneStatusDone && qa.QAVerdict == QAVerdictPass:
			return PlanPhaseDone
		}
	}
	for _, m := range ms {
		if m.Kind == MilestoneKindImplement && m.Status == MilestoneStatusFailed && m.Attempt >= MaxMilestoneAttempts {
			return PlanPhaseFailed
		}
	}
	return PlanPhaseImplementing
}

// DerivePlanKanban places a plan on the board from its phase. Any blocked
// milestone in a live phase reads as Blocked in the building lane.
func DerivePlanKanban(phase PlanPhase, ms []Milestone) (contract.KanbanColumn, contract.DisplayStatus) {
	switch phase {
	case PlanPhaseCancelled:
		return contract.KanbanArchive, contract.DisplayPlanCancelled
	case PlanPhasePlanning:
		return contract.KanbanBuilding, contract.DisplayPlanning
	case PlanPhaseDone:
		return contract.KanbanReady, contract.DisplayQAPassed
	}
	for _, m := range ms {
		if m.Status == MilestoneStatusBlocked {
			return contract.KanbanBuilding, contract.DisplayBlocked
		}
	}
	switch phase {
	case PlanPhaseFixing:
		return contract.KanbanBuilding, contract.DisplayFixingQAFindings
	case PlanPhaseQA:
		return contract.KanbanValidating, contract.DisplayTesting
	case PlanPhaseFailed:
		return contract.KanbanNeedsReview, contract.DisplayNeedsYou
	}
	done, total := 0, 0
	for _, m := range ms {
		if m.Kind != MilestoneKindImplement {
			continue
		}
		total++
		if m.Status == MilestoneStatusDone {
			done++
		}
	}
	return contract.KanbanBuilding, contract.DisplayStatus(fmt.Sprintf("%s %d/%d", contract.DisplayImplementing, done, total))
}
