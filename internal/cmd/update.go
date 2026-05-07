package cmd

import (
	"fmt"

	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

func newUpdateCmd() *cobra.Command {
	var (
		status      string
		title       string
		description string
		priority    int
		assignee    string
		dueDate     string
		parentID    string
	)

	cmd := &cobra.Command{
		Use:     "update <id>",
		Aliases: []string{"u"},
		Short:   "Update task fields",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := task.UpdateOpts{}

			if cmd.Flags().Changed("status") {
				s := task.Status(status)
				opts.Status = &s
			}
			if cmd.Flags().Changed("title") {
				opts.Title = &title
			}
			if cmd.Flags().Changed("description") {
				opts.Description = &description
			}
			if cmd.Flags().Changed("priority") {
				opts.Priority = &priority
			}
			if cmd.Flags().Changed("assignee") {
				opts.Assignee = &assignee
			}
			if cmd.Flags().Changed("due") {
				opts.DueDate = &dueDate
			}
			if cmd.Flags().Changed("parent") {
				opts.ParentID = &parentID
			}

			t, err := task.Update(db, args[0], opts)
			if err != nil {
				return err
			}
			if jsonOutput {
				return printJSON(t)
			}
			fmt.Printf("updated %s\n", t.ID)
			return nil
		},
	}

	cmd.Flags().StringVarP(&status, "status", "s", "", "New status (backlog, open, in_progress, done, cancelled)")
	cmd.Flags().StringVarP(&title, "title", "t", "", "New title")
	cmd.Flags().StringVarP(&description, "description", "d", "", "New description")
	cmd.Flags().IntVarP(&priority, "priority", "p", 0, "New priority (0=critical, 3=low)")
	cmd.Flags().StringVarP(&assignee, "assignee", "a", "", "New assignee")
	cmd.Flags().StringVarP(&dueDate, "due", "D", "", "New due date (YYYY-MM-DD)")
	cmd.Flags().StringVarP(&parentID, "parent", "P", "", "New parent task ID")

	return cmd
}
