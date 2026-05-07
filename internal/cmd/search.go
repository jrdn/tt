package cmd

import (
	"fmt"

	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

func newSearchCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "search <query>",
		Aliases: []string{"find"},
		Short:   "Search tasks by title, description, or comment",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tasks, err := task.Search(db, args[0])
			if err != nil {
				return err
			}
			if jsonOutput {
				return printJSON(tasks)
			}
			if len(tasks) == 0 {
				fmt.Println("no results")
				return nil
			}
			for _, t := range tasks {
				assignee := ""
				if t.Assignee != nil {
					assignee = "  @" + *t.Assignee
				}
				fmt.Printf("%-7s  %-12s  %s%s\n", t.ID, t.Status, t.Title, assignee)
			}
			return nil
		},
	}
}
