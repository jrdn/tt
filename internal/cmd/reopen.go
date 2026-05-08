package cmd

import (
	"fmt"

	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

func newReopenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reopen <id> [id...]",
		Short: "Set one or more tasks back to open",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			status := task.StatusOpen
			var last *task.Task
			for _, id := range args {
				t, err := task.Update(db, id, task.UpdateOpts{Status: &status})
				if err != nil {
					return err
				}
				last = t
				if !jsonOutput {
					fmt.Printf("%s reopened\n", t.ID)
				}
			}
			if jsonOutput {
				return printJSON(last)
			}
			return nil
		},
	}
}
