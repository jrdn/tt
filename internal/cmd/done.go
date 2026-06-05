package cmd

import (
	"fmt"

	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

func newDoneCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "done <id> [id...]",
		Aliases: []string{"d"},
		Short:   "Mark one or more tasks as done",
		Long: `Mark one or more tasks as done.

Each ID can be a plain task ID or a qualified ID (<db_name>.<task_id>).`,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			status := task.StatusDone
			var last *task.Task
			for _, rawID := range args {
				qt, err := resolveTask(rawID)
				if err != nil {
					return err
				}
				t, err := task.Update(qt.database, qt.id, task.UpdateOpts{Status: &status})
				qt.Close()
				if err != nil {
					return err
				}
				last = t
				if !jsonOutput {
					fmt.Printf("%s marked done\n", t.ID)
				}
			}
			if jsonOutput {
				return printJSON(last)
			}
			return nil
		},
	}
}
