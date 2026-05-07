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
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			status := task.StatusDone
			var last *task.Task
			for _, id := range args {
				t, err := task.Update(db, id, task.UpdateOpts{Status: &status})
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
