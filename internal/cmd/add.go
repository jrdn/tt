package cmd

import (
	"fmt"

	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

func newAddCmd() *cobra.Command {
	var (
		parent      string
		description string
		priority    int
		prioritySet bool
		createdBy   string
		assignee    string
		dueDate     string
	)

	cmd := &cobra.Command{
		Use:     "add <title>",
		Aliases: []string{"new", "n"},
		Short:   "Create a new task",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			title := args[0]
			opts := task.CreateOpts{
				Priority:    priority,
				PrioritySet: prioritySet,
			}
			if parent != "" {
				opts.ParentID = &parent
			}
			if description != "" {
				opts.Description = &description
			}
			if createdBy != "" {
				opts.CreatedBy = &createdBy
			}
			if assignee != "" {
				opts.Assignee = &assignee
			}
			if dueDate != "" {
				opts.DueDate = &dueDate
			}

			t, err := task.Create(db, title, opts)
			if err != nil {
				return err
			}
			if jsonOutput {
				return printJSON(t)
			}
			fmt.Printf("created %s\n", t.ID)
			return nil
		},
	}

	cmd.Flags().StringVar(&parent, "parent", "", "Parent task ID")
	cmd.Flags().StringVarP(&description, "description", "d", "", "Task description")
	cmd.Flags().IntVarP(&priority, "priority", "p", 2, "Priority 0-4 (0=critical, 4=backlog)")
	cmd.Flags().StringVar(&createdBy, "created-by", "", "Creator handle")
	cmd.Flags().StringVar(&assignee, "assignee", "", "Assignee handle (e.g. jrdn, claude/opus4.7)")
	cmd.Flags().StringVar(&dueDate, "due", "", "Due date (YYYY-MM-DD)")

	cmd.PreRun = func(cmd *cobra.Command, args []string) {
		prioritySet = cmd.Flags().Changed("priority")
	}

	return cmd
}
