package cmd

import (
	"fmt"

	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

func newRelateCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "relate <from-id> <type> <to-id>",
		Aliases: []string{"r"},
		Short:   "Add a relation between tasks (blocks, duplicates, related)",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			relType := args[1]
			switch relType {
			case "blocks", "duplicates", "related":
			default:
				return fmt.Errorf("unknown relation type %q (use: blocks, duplicates, related)", relType)
			}
			from, err := task.Get(db, args[0])
			if err != nil {
				return fmt.Errorf("from: %w", err)
			}
			to, err := task.Get(db, args[2])
			if err != nil {
				return fmt.Errorf("to: %w", err)
			}
			if err := task.AddRelation(db, args[0], relType, args[2]); err != nil {
				return err
			}
			if jsonOutput {
				return printJSON(task.Relation{FromID: from.ID, ToID: to.ID, Type: relType})
			}
			fmt.Printf("%s %s %s\n", args[0], relType, args[2])
			return nil
		},
	}
}
