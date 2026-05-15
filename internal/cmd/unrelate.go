package cmd

import (
	"fmt"

	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

func newUnrelateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unrelate <from-id> <type> <to-id>",
		Short: "Remove a relation between tasks",
		Long: `Remove a relation between tasks.

Each ID can be a plain task ID or a qualified ID (<db_name>.<task_id>).`,
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			relType := args[1]
			switch relType {
			case "blocks", "duplicates", "related":
			default:
				return fmt.Errorf("unknown relation type %q (use: blocks, duplicates, related)", relType)
			}
			fromQt, err := resolveTask(args[0])
			if err != nil {
				return err
			}
			defer fromQt.Close()
			toQt, err := resolveTask(args[2])
			if err != nil {
				return err
			}
			defer toQt.Close()
			if err := task.RemoveRelation(fromQt.database, fromQt.id, relType, toQt.id); err != nil {
				return err
			}
			fmt.Printf("removed %s %s %s\n", args[0], relType, args[2])
			return nil
		},
	}
}
