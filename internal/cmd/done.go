package cmd

import (
	"fmt"

	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

func newDoneCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "done <id>",
		Aliases: []string{"d"},
		Short:   "Mark a task as done",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			status := task.StatusDone
			t, err := task.Update(db, args[0], task.UpdateOpts{Status: &status})
			if err != nil {
				return err
			}
			if jsonOutput {
				return printJSON(t)
			}
			fmt.Printf("%s marked done\n", t.ID)
			return nil
		},
	}
}
