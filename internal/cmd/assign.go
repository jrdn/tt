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
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			handle := args[1]
			t, err := task.Update(db, args[0], task.UpdateOpts{Assignee: &handle})
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
