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
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			relType := args[1]
			switch relType {
			case "blocks", "duplicates", "related":
			default:
				return fmt.Errorf("unknown relation type %q (use: blocks, duplicates, related)", relType)
			}
			if err := task.RemoveRelation(db, args[0], relType, args[2]); err != nil {
				return err
			}
			fmt.Printf("removed %s %s %s\n", args[0], relType, args[2])
			return nil
		},
	}
}
