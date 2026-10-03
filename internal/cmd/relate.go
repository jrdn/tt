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
		Long: `Add a relation between tasks.

Each ID can be a plain task ID or a qualified ID (<db_name>.<task_id>).
Cross-database relations are not supported — both tasks must be in the same database.`,
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
				return fmt.Errorf("from: %w", err)
			}
			defer fromQt.Close()

			toQt, err := resolveTask(args[2])
			if err != nil {
				return fmt.Errorf("to: %w", err)
			}
			defer toQt.Close()

			from, err := fromQt.store.Get(cmd.Context(), fromQt.id)
			if err != nil {
				return fmt.Errorf("from: %w", err)
			}
			to, err := toQt.store.Get(cmd.Context(), toQt.id)
			if err != nil {
				return fmt.Errorf("to: %w", err)
			}
			if err := fromQt.store.AddRelation(cmd.Context(), fromQt.id, relType, toQt.id); err != nil {
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
