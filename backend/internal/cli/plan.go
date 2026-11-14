package cli

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// These DTOs mirror the daemon's task plan API without importing controller
// types.
type planDTO struct {
	ID                    string                `json:"id"`
	ProjectID             string                `json:"projectId"`
	Title                 string                `json:"title"`
	TicketRef             string                `json:"ticketRef,omitempty"`
	State                 string                `json:"state"`
	Phase                 string                `json:"phase"`
	KanbanColumn          string                `json:"kanbanColumn,omitempty"`
	DisplayStatus         string                `json:"displayStatus,omitempty"`
	MilestoneCounts       planMilestoneCountDTO `json:"milestoneCounts"`
	OrchestratorSessionID string                `json:"orchestratorSessionId,omitempty"`
	MaxParallel           int                   `json:"maxParallel,omitempty"`
	IntakeMode            string                `json:"intakeMode,omitempty"`
	DirectSessionID       string                `json:"directSessionId,omitempty"`
	Triage                *planTriageInfoDTO    `json:"triage,omitempty"`
	Brief                 string                `json:"brief,omitempty"`
	SpecMarkdown          string                `json:"specMarkdown,omitempty"`
}

type planTriageInfoDTO struct {
	Decision  string `json:"decision"`
	Reason    string `json:"reason,omitempty"`
	DecidedBy string `json:"decidedBy"`
	DecidedAt string `json:"decidedAt"`
}

type planMilestoneCountDTO struct {
	Total   int `json:"total"`
	Done    int `json:"done"`
	Running int `json:"running"`
	Failed  int `json:"failed"`
	Blocked int `json:"blocked"`
}

type planSessionDTO struct {
	ID            string `json:"id"`
	DisplayStatus string `json:"displayStatus,omitempty"`
}

type planMilestoneDTO struct {
	Key       string          `json:"key"`
	Kind      string          `json:"kind"`
	Title     string          `json:"title"`
	Status    string          `json:"status"`
	DependsOn []string        `json:"dependsOn"`
	Attempt   int             `json:"attempt,omitempty"`
	Session   *planSessionDTO `json:"session,omitempty"`
	Branch    string          `json:"branch,omitempty"`
}

type planEventDTO struct {
	ID           int64  `json:"id"`
	MilestoneKey string `json:"milestoneKey"`
	SessionID    string `json:"sessionId,omitempty"`
	Kind         string `json:"kind"`
	Note         string `json:"note"`
	CreatedAt    string `json:"createdAt"`
}

type planEnvelopeDTO struct {
	Plan planDTO `json:"plan"`
}

type planListDTO struct {
	Plans []planDTO `json:"plans"`
}

type planDetailDTO struct {
	Plan       planDTO            `json:"plan"`
	Milestones []planMilestoneDTO `json:"milestones"`
	Events     []planEventDTO     `json:"events"`
}

type planMilestoneEnvelopeDTO struct {
	Milestone planMilestoneDTO `json:"milestone"`
}

type planEventsDTO struct {
	Events    []planEventDTO `json:"events"`
	NextAfter int64          `json:"nextAfter"`
}

type planCreateDTO struct {
	Title       string `json:"title"`
	Brief       string `json:"brief"`
	TicketRef   string `json:"ticketRef,omitempty"`
	MaxParallel int    `json:"maxParallel,omitempty"`
	Mode        string `json:"mode,omitempty"`
}

type planTriageDTO struct {
	Decision  string `json:"decision"`
	Reason    string `json:"reason,omitempty"`
	DecidedBy string `json:"decidedBy"`
}

// planTriageReasonMaxBytes mirrors the daemon's triage reason limit.
const planTriageReasonMaxBytes = 500

type planMilestoneRequestDTO struct {
	Key         *string   `json:"key,omitempty"`
	Kind        *string   `json:"kind,omitempty"`
	Title       *string   `json:"title,omitempty"`
	Description *string   `json:"description,omitempty"`
	DependsOn   *[]string `json:"dependsOn,omitempty"`
	SparsePaths *[]string `json:"sparsePaths,omitempty"`
}

type planQAResultDTO struct {
	Verdict      string `json:"verdict"`
	MilestoneKey string `json:"milestoneKey,omitempty"`
	Note         string `json:"note"`
	SessionID    string `json:"sessionId"`
}

const planIDHelp = "<plan-id> may be omitted inside a plan session, where AO_PLAN_ID supplies it."

func newPlanCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "plan",
		Aliases: []string{"plans"},
		Short:   "Plan a Task into milestones that AO runs as workers and verifies with QA",
		Long: "Plans (Tasks in the app) turn a ticket into milestones. The project orchestrator writes the plan and " +
			"registers milestones; after activation AO spawns a worker per milestone in dependency order and a QA " +
			"worker once every milestone is done.\n\n" + planIDHelp,
	}
	milestone := &cobra.Command{Use: "milestone", Aliases: []string{"milestones"}, Short: "Add, edit, remove, or retry a plan's milestones"}
	milestone.AddCommand(
		newPlanMilestoneAddCommand(ctx), newPlanMilestoneUpdateCommand(ctx),
		newPlanMilestoneRemoveCommand(ctx), newPlanMilestoneRetryCommand(ctx),
	)
	cmd.AddCommand(
		newPlanCreateCommand(ctx), newPlanListCommand(ctx), newPlanGetCommand(ctx), newPlanSetSpecCommand(ctx),
		milestone, newPlanActivateCommand(ctx), newPlanCancelCommand(ctx), newPlanQAResultCommand(ctx),
		newPlanTriageCommand(ctx),
		newPlanEventsCommand(ctx),
	)
	return cmd
}

// planIDArg resolves the plan id from the first positional argument or
// AO_PLAN_ID. It returns the remaining positional arguments. want is the
// number of positional arguments besides the plan id.
func planIDArg(args []string, want int) (string, []string, error) {
	switch len(args) {
	case want + 1:
		if strings.TrimSpace(args[0]) == "" {
			return "", nil, usageError{errors.New("plan id is required")}
		}
		return args[0], args[1:], nil
	case want:
		if id := strings.TrimSpace(os.Getenv("AO_PLAN_ID")); id != "" {
			return id, args, nil
		}
		return "", nil, usageError{errors.New("plan id is required (pass it or set AO_PLAN_ID)")}
	default:
		return "", nil, usageError{fmt.Errorf("expected %d argument(s) after the plan id, got %d", want, len(args))}
	}
}

func planPath(id string, rest ...string) string {
	parts := append([]string{"plans", url.PathEscape(id)}, rest...)
	return strings.Join(parts, "/")
}

// textOrFile returns the inline value or the named file's contents. Exactly
// one of the two must be given when required is true.
func textOrFile(name, inline, file string, inlineSet, fileSet, required bool) (string, bool, error) {
	if inlineSet && fileSet {
		return "", false, usageError{fmt.Errorf("provide only one of --%s or --%s-file", name, name)}
	}
	if fileSet {
		data, err := os.ReadFile(file) //nolint:gosec // the person names the file to read
		if err != nil {
			return "", false, usageError{fmt.Errorf("read --%s-file: %w", name, err)}
		}
		return string(data), true, nil
	}
	if inlineSet {
		return inline, true, nil
	}
	if required {
		return "", false, usageError{fmt.Errorf("--%s or --%s-file is required", name, name)}
	}
	return "", false, nil
}

func newPlanCreateCommand(ctx *commandContext) *cobra.Command {
	var project, title, brief, briefFile, ticket, mode string
	var maxParallel int
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a Task plan and ask the project orchestrator to plan it",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(title) == "" {
				return usageError{errors.New("--title is required")}
			}
			text, _, err := textOrFile("brief", brief, briefFile, cmd.Flags().Changed("brief"), cmd.Flags().Changed("brief-file"), true)
			if err != nil {
				return err
			}
			if strings.TrimSpace(text) == "" {
				return usageError{errors.New("the brief must not be empty")}
			}
			if cmd.Flags().Changed("max-parallel") && (maxParallel < 1 || maxParallel > 10) {
				return usageError{errors.New("--max-parallel must be 1-10")}
			}
			switch mode {
			case "", "plan", "auto":
			default:
				return usageError{errors.New("--mode must be plan or auto (a direct Task is `ao spawn`)")}
			}
			resolved, err := ctx.resolveSpawnProject(cmd.Context(), project)
			if err != nil {
				return err
			}
			var response planEnvelopeDTO
			body := planCreateDTO{Title: title, Brief: text, TicketRef: ticket, MaxParallel: maxParallel, Mode: mode}
			if err := ctx.postJSON(cmd.Context(), "projects/"+url.PathEscape(resolved.ID)+"/plans", body, &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			orchestrator := response.Plan.OrchestratorSessionID
			if orchestrator == "" {
				orchestrator = "(starting)"
			}
			doing := "planning it"
			if response.Plan.State == "triage" {
				doing = "deciding how to run it"
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "created plan %s in project %s; orchestrator %s is %s\n",
				response.Plan.ID, response.Plan.ProjectID, orchestrator, doing)
			return err
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Project id (default: AO_PROJECT_ID, current session, or current registered repo)")
	cmd.Flags().StringVar(&title, "title", "", "Task title")
	cmd.Flags().StringVar(&brief, "brief", "", "Ticket text")
	cmd.Flags().StringVar(&briefFile, "brief-file", "", "Read the ticket text from a file")
	cmd.Flags().StringVar(&ticket, "ticket", "", "Ticket link or reference, shown in the app")
	cmd.Flags().IntVar(&maxParallel, "max-parallel", 0, "Most implementation workers running at once (1-10, default 3)")
	cmd.Flags().StringVar(&mode, "mode", "", "plan (default) plans the Task; auto lets the orchestrator decide between a plan and one worker")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func newPlanListCommand(ctx *commandContext) *cobra.Command {
	var project string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List a project's plans, newest first",
		Args:    usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := ctx.resolveSpawnProject(cmd.Context(), project)
			if err != nil {
				return err
			}
			var response planListDTO
			if err := ctx.getJSON(cmd.Context(), "projects/"+url.PathEscape(resolved.ID)+"/plans", &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(writer, "ID\tPHASE\tSTATUS\tMILESTONES\tTITLE")
			for _, p := range response.Plans {
				_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%d/%d\t%s\n", p.ID, p.Phase, p.DisplayStatus,
					p.MilestoneCounts.Done, p.MilestoneCounts.Total, p.Title)
			}
			return writer.Flush()
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Project id (default: AO_PROJECT_ID, current session, or current registered repo)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func newPlanGetCommand(ctx *commandContext) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "get [<plan-id>]",
		Short: "Show a plan's phase, milestones, and latest events",
		Long:  planIDHelp,
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, _, err := planIDArg(args, 0)
			if err != nil {
				return err
			}
			var response planDetailDTO
			if err := ctx.getJSON(cmd.Context(), planPath(id), &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			return writePlanDetail(cmd.OutOrStdout(), response)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func writePlanDetail(out io.Writer, d planDetailDTO) error {
	p := d.Plan
	if _, err := fmt.Fprintf(out, "%s  %s\nphase: %s (%s)  state: %s  max parallel: %d  orchestrator: %s\n\n",
		p.ID, p.Title, p.Phase, p.DisplayStatus, p.State, p.MaxParallel, dashIfEmpty(p.OrchestratorSessionID)); err != nil {
		return err
	}
	if line := planTriageLine(p); line != "" {
		if _, err := fmt.Fprintf(out, "%s\n\n", line); err != nil {
			return err
		}
	}
	writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "KEY\tKIND\tSTATUS\tDEPS\tSESSION")
	for _, m := range d.Milestones {
		session := "-"
		if m.Session != nil && m.Session.ID != "" {
			session = m.Session.ID
		}
		deps := strings.Join(m.DependsOn, ",")
		_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n", m.Key, m.Kind, m.Status, dashIfEmpty(deps), session)
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	if len(d.Events) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(out, "\nrecent events:"); err != nil {
		return err
	}
	const shown = 10
	for i, e := range d.Events {
		if i == shown {
			break
		}
		line := fmt.Sprintf("  %s  %s", e.CreatedAt, e.Kind)
		if e.MilestoneKey != "" {
			line += " " + e.MilestoneKey
		}
		if e.Note != "" {
			line += ": " + e.Note
		}
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}
	return nil
}

// planTriageLine describes an auto Task's triage: waiting, or the decision
// and, for direct, the worker it went to.
func planTriageLine(p planDTO) string {
	if p.IntakeMode != "auto" {
		return ""
	}
	if p.Triage == nil {
		return "triage: waiting for the orchestrator to decide (ao plan triage " + p.ID + " --plan|--direct)"
	}
	line := fmt.Sprintf("triage: %s, decided by %s", p.Triage.Decision, p.Triage.DecidedBy)
	if p.Triage.Reason != "" {
		line += ": " + p.Triage.Reason
	}
	if p.DirectSessionID != "" {
		line += "\nworker session: " + p.DirectSessionID
	}
	return line
}

func dashIfEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func newPlanSetSpecCommand(ctx *commandContext) *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "set-spec [<plan-id>] --file <path>",
		Short: "Store the plan document (Markdown) while the plan is planning",
		Long:  planIDHelp,
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(file) == "" {
				return usageError{errors.New("--file is required")}
			}
			id, _, err := planIDArg(args, 0)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(file) //nolint:gosec // the person names the file to read
			if err != nil {
				return usageError{fmt.Errorf("read --file: %w", err)}
			}
			spec := string(data)
			var response planEnvelopeDTO
			if err := ctx.patchJSON(cmd.Context(), planPath(id), map[string]any{"specMarkdown": spec}, &response); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "stored plan document for %s (%d bytes)\n", id, len(spec))
			return err
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "Markdown file holding the plan")
	return cmd
}

type planMilestoneFlags struct {
	key, kind, title, description, descriptionFile string
	dependsOn, sparse                              []string
}

func bindPlanMilestoneFlags(cmd *cobra.Command, f *planMilestoneFlags, withKey bool) {
	if withKey {
		cmd.Flags().StringVar(&f.key, "key", "", "Milestone key, e.g. m1 (lowercase letters, digits, dashes; qa is reserved)")
	} else {
		cmd.Flags().StringVar(&f.key, "key", "", "Rename the milestone key")
	}
	cmd.Flags().StringVar(&f.kind, "kind", "", "implement (default) or qa")
	cmd.Flags().StringVar(&f.title, "title", "", "Milestone title")
	cmd.Flags().StringVar(&f.description, "description", "", "The milestone's own spec, Markdown")
	cmd.Flags().StringVar(&f.descriptionFile, "description-file", "", "Read the description from a file")
	cmd.Flags().StringSliceVar(&f.dependsOn, "depends-on", nil, "Comma-separated keys this milestone needs (empty clears)")
	cmd.Flags().StringArrayVar(&f.sparse, "sparse", nil, "Repo-relative directory for a sparse checkout (repeatable)")
}

// milestoneBody builds the request from the flags the person set, so an
// update only sends changed fields.
func milestoneBody(cmd *cobra.Command, f *planMilestoneFlags) (planMilestoneRequestDTO, error) {
	var body planMilestoneRequestDTO
	changed := cmd.Flags().Changed
	if changed("key") {
		body.Key = &f.key
	}
	if changed("kind") {
		if f.kind != "implement" && f.kind != "qa" {
			return body, usageError{errors.New("--kind must be implement or qa")}
		}
		body.Kind = &f.kind
	}
	if changed("title") {
		body.Title = &f.title
	}
	desc, set, err := textOrFile("description", f.description, f.descriptionFile, changed("description"), changed("description-file"), false)
	if err != nil {
		return body, err
	}
	if set {
		body.Description = &desc
	}
	if changed("depends-on") {
		deps := make([]string, 0, len(f.dependsOn))
		for _, d := range f.dependsOn {
			if d = strings.TrimSpace(d); d != "" {
				deps = append(deps, d)
			}
		}
		body.DependsOn = &deps
	}
	if changed("sparse") {
		paths := append([]string{}, f.sparse...)
		body.SparsePaths = &paths
	}
	return body, nil
}

func newPlanMilestoneAddCommand(ctx *commandContext) *cobra.Command {
	var f planMilestoneFlags
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "add [<plan-id>] --key <k> --title <t> (--description <text> | --description-file <path>)",
		Short: "Add a milestone to a plan that is still planning",
		Long:  planIDHelp,
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(f.key) == "" {
				return usageError{errors.New("--key is required")}
			}
			if strings.TrimSpace(f.title) == "" {
				return usageError{errors.New("--title is required")}
			}
			body, err := milestoneBody(cmd, &f)
			if err != nil {
				return err
			}
			if body.Kind == nil {
				kind := "implement"
				body.Kind = &kind
			}
			if *body.Kind == "implement" && (body.Description == nil || strings.TrimSpace(*body.Description) == "") {
				return usageError{errors.New("--description or --description-file is required for an implement milestone")}
			}
			id, _, err := planIDArg(args, 0)
			if err != nil {
				return err
			}
			var response planMilestoneEnvelopeDTO
			if err := ctx.postJSON(cmd.Context(), planPath(id, "milestones"), body, &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "added milestone %s to plan %s\n", response.Milestone.Key, id)
			return err
		},
	}
	bindPlanMilestoneFlags(cmd, &f, true)
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func newPlanMilestoneUpdateCommand(ctx *commandContext) *cobra.Command {
	var f planMilestoneFlags
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "update [<plan-id>] <key> [flags]",
		Short: "Edit a milestone of a plan that is still planning; only the flags you pass change",
		Long:  planIDHelp,
		Args:  usageArgs(cobra.RangeArgs(1, 2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, rest, err := planIDArg(args, 1)
			if err != nil {
				return err
			}
			body, err := milestoneBody(cmd, &f)
			if err != nil {
				return err
			}
			if body == (planMilestoneRequestDTO{}) {
				return usageError{errors.New("nothing to update: pass at least one flag")}
			}
			var response planMilestoneEnvelopeDTO
			if err := ctx.patchJSON(cmd.Context(), planPath(id, "milestones", url.PathEscape(rest[0])), body, &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "updated milestone %s of plan %s\n", response.Milestone.Key, id)
			return err
		},
	}
	bindPlanMilestoneFlags(cmd, &f, false)
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func newPlanMilestoneRemoveCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:     "rm [<plan-id>] <key>",
		Aliases: []string{"remove"},
		Short:   "Remove a milestone from a plan that is still planning",
		Long:    planIDHelp,
		Args:    usageArgs(cobra.RangeArgs(1, 2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, rest, err := planIDArg(args, 1)
			if err != nil {
				return err
			}
			if err := ctx.deleteJSON(cmd.Context(), planPath(id, "milestones", url.PathEscape(rest[0])), nil); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "removed milestone %s from plan %s\n", rest[0], id)
			return err
		},
	}
}

func newPlanMilestoneRetryCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "retry [<plan-id>] <key>",
		Short: "Retry a failed milestone, or re-run QA (key qa), with a fresh attempt budget",
		Long:  planIDHelp,
		Args:  usageArgs(cobra.RangeArgs(1, 2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, rest, err := planIDArg(args, 1)
			if err != nil {
				return err
			}
			var response planDetailDTO
			if err := ctx.postJSON(cmd.Context(), planPath(id, "milestones", url.PathEscape(rest[0]), "retry"), nil, &response); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "milestone %s of plan %s is ready to run again\n", rest[0], id)
			return err
		},
	}
}

func newPlanActivateCommand(ctx *commandContext) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "activate [<plan-id>]",
		Short: "Validate the milestone graph and start the plan",
		Long:  "Activation adds the QA milestone when none was registered, then AO starts workers.\n\n" + planIDHelp,
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, _, err := planIDArg(args, 0)
			if err != nil {
				return err
			}
			var response planDetailDTO
			if err := ctx.postJSON(cmd.Context(), planPath(id, "activate"), nil, &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			return writePlanDetail(cmd.OutOrStdout(), response)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func newPlanCancelCommand(ctx *commandContext) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "cancel [<plan-id>]",
		Short: "Cancel a plan; its sessions keep running",
		Long:  planIDHelp,
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, _, err := planIDArg(args, 0)
			if err != nil {
				return err
			}
			if !yes {
				if !stdinIsInteractive(cmd.InOrStdin()) {
					return usageError{errors.New("pass --yes to cancel a plan without a prompt")}
				}
				ok, err := confirm(cmd.InOrStdin(), cmd.OutOrStdout(), fmt.Sprintf("Cancel plan %s?", id), false)
				if err != nil {
					return err
				}
				if !ok {
					_, err := fmt.Fprintln(cmd.OutOrStdout(), "kept the plan")
					return err
				}
			}
			var response planEnvelopeDTO
			if err := ctx.postJSON(cmd.Context(), planPath(id, "cancel"), nil, &response); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "plan %s is %s\n", id, response.Plan.State)
			return err
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Cancel without asking")
	return cmd
}

func newPlanQAResultCommand(ctx *commandContext) *cobra.Command {
	var pass, fail bool
	var milestone, note string
	cmd := &cobra.Command{
		Use:   "qa-result [<plan-id>] (--pass | --fail --milestone <key>) --note <text>",
		Short: "Record the QA verdict (QA sessions only)",
		Long:  "Only the plan's QA session may record a result; the session id comes from AO_SESSION_ID.\n\n" + planIDHelp,
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if pass == fail {
				return usageError{errors.New("provide exactly one of --pass or --fail")}
			}
			if fail && strings.TrimSpace(milestone) == "" && !cmd.Flags().Changed("milestone") {
				return usageError{errors.New("--fail needs --milestone <key> (pass --milestone \"\" when no milestone owns the defect)")}
			}
			if pass && strings.TrimSpace(milestone) != "" {
				return usageError{errors.New("--pass covers the whole plan; drop --milestone")}
			}
			if strings.TrimSpace(note) == "" {
				return usageError{errors.New("--note is required")}
			}
			sessionID := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
			if sessionID == "" {
				return usageError{errors.New("AO_SESSION_ID is not set; run this from the plan's QA session")}
			}
			id, _, err := planIDArg(args, 0)
			if err != nil {
				return err
			}
			body := planQAResultDTO{Verdict: "pass", Note: note, SessionID: sessionID}
			if fail {
				body.Verdict, body.MilestoneKey = "fail", strings.TrimSpace(milestone)
			}
			var response planDetailDTO
			if err := ctx.postJSON(cmd.Context(), planPath(id, "qa-result"), body, &response); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "recorded QA %s for plan %s; phase is %s\n", body.Verdict, id, response.Plan.Phase)
			return err
		},
	}
	cmd.Flags().BoolVar(&pass, "pass", false, "Everything works")
	cmd.Flags().BoolVar(&fail, "fail", false, "Record a defect against a milestone")
	cmd.Flags().StringVar(&milestone, "milestone", "", "The milestone whose change caused the defect")
	cmd.Flags().StringVar(&note, "note", "", "What you verified, or steps to reproduce, expected, actual, and file hints")
	return cmd
}

func newPlanTriageCommand(ctx *commandContext) *cobra.Command {
	var asPlan, direct, jsonOutput bool
	var reason, by string
	cmd := &cobra.Command{
		Use:   "triage [<plan-id>] (--plan | --direct) [--reason <text>]",
		Short: "Decide how an auto Task runs: as a plan, or as one worker",
		Long: "--plan turns the Task into an ordinary plan to set a spec for, add milestones to, and activate. --direct " +
			"spawns one worker with the Task's brief and closes the plan. A Task is decided once.\n\n" +
			"--by defaults to orchestrator inside an AO session (AO_SESSION_ID set) and human otherwise.\n\n" + planIDHelp,
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if asPlan == direct {
				return usageError{errors.New("provide exactly one of --plan or --direct")}
			}
			if len(strings.TrimSpace(reason)) > planTriageReasonMaxBytes {
				return usageError{fmt.Errorf("--reason must be at most %d bytes", planTriageReasonMaxBytes)}
			}
			if !cmd.Flags().Changed("by") {
				by = "human"
				if strings.TrimSpace(os.Getenv("AO_SESSION_ID")) != "" {
					by = "orchestrator"
				}
			}
			if by != "orchestrator" && by != "human" {
				return usageError{errors.New("--by must be orchestrator or human")}
			}
			id, _, err := planIDArg(args, 0)
			if err != nil {
				return err
			}
			body := planTriageDTO{Decision: "plan", Reason: strings.TrimSpace(reason), DecidedBy: by}
			if direct {
				body.Decision = "direct"
			}
			var response planEnvelopeDTO
			if err := ctx.postJSON(cmd.Context(), planPath(id, "triage"), body, &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			line := fmt.Sprintf("plan %s decided: %s; phase is %s", id, body.Decision, response.Plan.Phase)
			if response.Plan.DirectSessionID != "" {
				line += "; worker session " + response.Plan.DirectSessionID
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), line)
			return err
		},
	}
	cmd.Flags().BoolVar(&asPlan, "plan", false, "Run the Task as a plan: set a spec, add milestones, activate")
	cmd.Flags().BoolVar(&direct, "direct", false, "Hand the Task to one worker with its brief")
	cmd.Flags().StringVar(&reason, "reason", "", "One sentence on why (at most 500 bytes)")
	cmd.Flags().StringVar(&by, "by", "", "Who decided: orchestrator or human (default: orchestrator inside an AO session)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func newPlanEventsCommand(ctx *commandContext) *cobra.Command {
	var after int64
	var limit int
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "events [<plan-id>]",
		Short: "Print a plan's activity log, oldest first",
		Long:  planIDHelp,
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if after < 0 {
				return usageError{errors.New("--after must not be negative")}
			}
			if cmd.Flags().Changed("limit") && (limit < 1 || limit > 500) {
				return usageError{errors.New("--limit must be 1-500")}
			}
			id, _, err := planIDArg(args, 0)
			if err != nil {
				return err
			}
			q := url.Values{}
			if cmd.Flags().Changed("after") {
				q.Set("after", strconv.FormatInt(after, 10))
			}
			if cmd.Flags().Changed("limit") {
				q.Set("limit", strconv.Itoa(limit))
			}
			path := planPath(id, "events")
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			var response planEventsDTO
			if err := ctx.getJSON(cmd.Context(), path, &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(writer, "ID\tTIME\tKIND\tMILESTONE\tNOTE")
			for _, e := range response.Events {
				_, _ = fmt.Fprintf(writer, "%d\t%s\t%s\t%s\t%s\n", e.ID, e.CreatedAt, e.Kind, dashIfEmpty(e.MilestoneKey), e.Note)
			}
			return writer.Flush()
		},
	}
	cmd.Flags().Int64Var(&after, "after", 0, "Only events with an id greater than this")
	cmd.Flags().IntVar(&limit, "limit", 0, "Most events to print (1-500, default 100)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}
