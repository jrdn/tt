package cmd

import (
	"fmt"

	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

func newAssignCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "assign <id> <handle>",
		Short: "Assign a task to a handle",
		Long: `Assign a task to a handle.

The ID can be a plain task ID or a qualified ID (<db_name>.<task_id>).`,
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			qt, err := resolveTask(args[0])
			if err != nil {
				return err
			}
			defer qt.Close()
			handle := args[1]
			t, err := task.Update(qt.database, qt.id, task.UpdateOpts{Assignee: &handle})
			if err != nil {
				return err
			}
			if jsonOutput {
				return printJSON(t)
			}
			fmt.Printf("%s assigned to %s\n", t.ID, handle)
			return nil
		},
	}
}
