package cli

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

// boardLanes are the lanes `--to` accepts, in board order.
var boardLanes = []string{"queued", "building", "validating", "needs_review", "needs_you", "ready"}

type boardMoveDTO struct {
	To string `json:"to"`
}

func boardLaneArg(lane string) (string, error) {
	lane = strings.TrimSpace(lane)
	for _, known := range boardLanes {
		if lane == known {
			return lane, nil
		}
	}
	return "", usageError{fmt.Errorf("--to must be one of %s", strings.Join(boardLanes, ", "))}
}

// boardLaneText prints a lane, and for needs_you the stage behind it:
// "needs_you (in review)".
func boardLaneText(column, stage string) string {
	if column == "" {
		return "-"
	}
	if column != "needs_you" || stage == "" || stage == column {
		return column
	}
	return fmt.Sprintf("%s (%s)", column, boardStageNames[stage])
}

var boardStageNames = map[string]string{
	"queued": "in queue", "building": "building", "validating": "validating",
	"needs_review": "in review", "ready": "ready", "archive": "archive",
}

func newSessionMoveCommand(ctx *commandContext) *cobra.Command {
	var to string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "move <id> --to <lane>",
		Short: "Move a session's board card to a lane",
		Long: "A move never sets a column: it changes a fact or sends the agent an instruction, and the card lands " +
			"where the facts then put it. needs_you asks the agent to pause and summarise; building releases it; " +
			"validating asks it to deliver or hands its PR back to AO; needs_review hands the PR to a person; ready " +
			"accepts work that has no PR. A move that cannot change anything is refused with the reason.\n\n" +
			"Lanes: " + strings.Join(boardLanes, ", ") + ".",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			lane, err := boardLaneArg(to)
			if err != nil {
				return err
			}
			id, err := normalizeSessionID(args[0])
			if err != nil {
				return err
			}
			var response struct {
				Session sessionDTO `json:"session"`
			}
			if err := ctx.postJSON(cmd.Context(), "sessions/"+url.PathEscape(id)+"/board-move", boardMoveDTO{To: lane}, &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			s := response.Session
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "session %s is in %s: %s\n", s.ID, boardLaneText(s.KanbanColumn, s.KanbanStage), emptyDash(s.DisplayStatus))
			return err
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "The lane: "+strings.Join(boardLanes, ", "))
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

func newPlanMoveCommand(ctx *commandContext) *cobra.Command {
	var to string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "move [<plan-id>] --to <lane>",
		Short: "Put a Task on hold, ask it to pause, or release it",
		Long: "queued holds the Task: no milestone worker starts until it is released (only while triaging, planning, " +
			"or with no milestone running). needs_you asks the orchestrator to pause the Task and summarise. building, " +
			"or the Task's own lane, releases it. QA decides validating and ready.\n\n" + planIDHelp,
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			lane, err := boardLaneArg(to)
			if err != nil {
				return err
			}
			id, _, err := planIDArg(args, 0)
			if err != nil {
				return err
			}
			var response planDetailDTO
			if err := ctx.postJSON(cmd.Context(), planPath(id, "board-move"), boardMoveDTO{To: lane}, &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			p := response.Plan
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "plan %s is in %s: %s\n", p.ID, boardLaneText(p.KanbanColumn, p.KanbanStage), emptyDash(p.DisplayStatus))
			return err
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "The lane: "+strings.Join(boardLanes, ", "))
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

func newAutomationRunNowCommand(ctx *commandContext) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "run-now <id>",
		Short: "Queue a run of an enabled automation now",
		Long:  "The scheduled run still happens; its next run time is unchanged. Refused while the automation is off or a run is already in progress.",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			var run automationRunSummaryDTO
			if err := ctx.postJSON(cmd.Context(), "automations/"+url.PathEscape(args[0])+"/run-now", nil, &run); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), run)
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "queued run %s of automation %s for %s\n", run.ID, args[0], formatAutomationTime(run.ScheduledFor))
			return err
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}
