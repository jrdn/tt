package cmd

import (
	"fmt"

	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

func newAssignCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "assign <handle> <id> [id...]",
		Short: "Assign one or more tasks to a handle",
		Long: `Assign one or more tasks to a handle.

Each ID can be a plain task ID or a qualified ID (<db_name>.<task_id>).`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			handle := args[0]
			var tasks []any
			for _, id := range args[1:] {
				qt, err := resolveTask(id)
				if err != nil {
					return err
				}
				t, err := task.Update(qt.database, qt.id, task.UpdateOpts{Assignee: &handle})
				qt.Close()
				if err != nil {
					return err
				}
				if jsonOutput {
						tasks = append(tasks, t)
				} else {
					fmt.Printf("%s assigned to %s\n", t.ID, handle)
				}
			}
			if jsonOutput {
				if len(tasks) == 1 {
					return printJSON(tasks[0])
				}
				return printJSON(tasks)
			}
			return nil
		},
	}
}
