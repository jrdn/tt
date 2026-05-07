package cmd

import (
	"fmt"

	"github.com/fatih/color"
	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

var (
	statusColors = map[task.Status]*color.Color{
		task.StatusBacklog:    color.New(color.FgHiBlack),
		task.StatusOpen:       color.New(color.FgCyan),
		task.StatusInProgress: color.New(color.FgBlue, color.Bold),
		task.StatusDone:       color.New(color.FgGreen),
		task.StatusCancelled:  color.New(color.Faint),
	}
	priorityColors = [4]*color.Color{
		color.New(color.FgRed, color.Bold),    // 0 critical
		color.New(color.FgYellow, color.Bold), // 1 high
		color.New(color.Reset),                // 2 normal
		color.New(color.FgHiBlack),            // 3 low
	}
	priorityLabels = [4]string{"●", "◕", "◑", "◔"}
)

func newLsCmd() *cobra.Command {
	var (
		status   string
		parentID string
		all      bool
		ready    bool
	)

	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"l"},
		Short:   "List tasks (open and in_progress by default)",
		RunE: func(cmd *cobra.Command, args []string) error {
			tasks, err := task.List(db, task.ListOpts{
				Status:   status,
				ParentID: parentID,
				All:      all,
				Ready:    ready,
			})
			if err != nil {
				return err
			}
			if jsonOutput {
				return printJSON(tasks)
			}
			if len(tasks) == 0 {
				fmt.Println("no tasks")
				return nil
			}
			header := color.New(color.Faint)
			header.Printf("%-6s  %s  %-13s  %s\n", "ID", "P", "STATUS", "TITLE")
			for _, t := range tasks {
				assignee := ""
				if t.Assignee != nil {
					assignee = color.New(color.FgHiBlack).Sprint("  @" + *t.Assignee)
				}

				p := t.Priority
				if p < 0 || p > 3 {
					p = 2
				}
				priLabel := priorityColors[p].Sprint(priorityLabels[p])

				sc, ok := statusColors[t.Status]
				if !ok {
					sc = color.New(color.Reset)
				}
				statusStr := sc.Sprintf("%-13s", string(t.Status))

				fmt.Printf("%-6s  %s  %s  %s%s\n",
					t.ID,
					priLabel,
					statusStr,
					t.Title,
					assignee,
				)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&status, "status", "", "Filter by status (backlog, open, in_progress, done, cancelled)")
	cmd.Flags().StringVar(&parentID, "parent", "", "Show subtasks of this task")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Include all statuses")
	cmd.Flags().BoolVarP(&ready, "ready", "r", false, "Open tasks with no unresolved blockers")

	return cmd
}
