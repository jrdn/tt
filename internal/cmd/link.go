package cmd

import (
	"fmt"

	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

func newLinkCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "link <id> <source> <external-id> [url]",
		Short: "Link a task to an external resource (e.g. a Linear issue)",
		Long: `Link a task to an external resource.

The ID can be a plain task ID or a qualified ID (<db_name>.<task_id>).
Re-running with the same source and external-id updates the URL and
re-points the ref at this task.`,
		Args: cobra.RangeArgs(3, 4),
		RunE: func(cmd *cobra.Command, args []string) error {
			qt, err := resolveTask(args[0])
			if err != nil {
				return err
			}
			defer qt.Close()

			source, externalID := args[1], args[2]
			var url *string
			if len(args) == 4 {
				url = &args[3]
			}

			if err := task.UpsertExternalRef(qt.database, qt.id, source, externalID, url); err != nil {
				return err
			}

			if jsonOutput {
				return printJSON(task.ExternalRef{TaskID: qt.id, Source: source, ExternalID: externalID, URL: url})
			}
			fmt.Printf("linked %s to %s:%s\n", args[0], source, externalID)
			return nil
		},
	}
}
