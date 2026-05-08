package cmd

import (
	"fmt"

	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

func newCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <id> [id...]",
		Short: "Mark one or more tasks as cancelled",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			status := task.StatusCancelled
			var last *task.Task
			for _, id := range args {
				t, err := task.Update(db, id, task.UpdateOpts{Status: &status})
				if err != nil {
					return err
				}
				last = t
				if !jsonOutput {
					fmt.Printf("%s cancelled\n", t.ID)
				}
			}
			if jsonOutput {
				return printJSON(last)
			}
			return nil
		},
	}
}
