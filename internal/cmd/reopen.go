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
		Long: `Set one or more tasks back to open.

Each ID can be a plain task ID or a qualified ID (<db_name>.<task_id>).`,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			status := task.StatusOpen
			var last *task.Task
			for _, rawID := range args {
				qt, err := resolveTask(rawID)
				if err != nil {
					return err
				}
				t, err := qt.store.Update(cmd.Context(), qt.id, task.UpdateOpts{Status: &status})
				qt.Close()
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
