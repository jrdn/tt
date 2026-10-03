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
		Use:     "update <id> [id...]",
		Aliases: []string{"u"},
		Short:   "Update fields on one or more tasks",
		Long: `Update fields on one or more tasks.

Each ID can be a plain task ID or a qualified ID (<db_name>.<task_id>).`,
		Args:    cobra.MinimumNArgs(1),
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

			var last *task.Task
			for _, rawID := range args {
				qt, err := resolveTask(rawID)
				if err != nil {
					return err
				}
				t, err := qt.store.Update(cmd.Context(), qt.id, opts)
				qt.Close()
				if err != nil {
					return err
				}
				last = t
				if !jsonOutput {
					fmt.Printf("updated %s\n", t.ID)
				}
			}
			if jsonOutput {
				return printJSON(last)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&status, "status", "s", "", "New status (backlog, open, ready, in_progress, in_review, done, cancelled)")
	cmd.Flags().StringVarP(&title, "title", "t", "", "New title")
	cmd.Flags().StringVarP(&description, "description", "d", "", "New description")
	cmd.Flags().IntVarP(&priority, "priority", "p", 0, "New priority (0=critical, 3=low)")
	cmd.Flags().StringVarP(&assignee, "assignee", "a", "", "New assignee")
	cmd.Flags().StringVarP(&dueDate, "due", "D", "", "New due date (YYYY-MM-DD)")
	cmd.Flags().StringVarP(&parentID, "parent", "P", "", "New parent task ID")

	return cmd
}
