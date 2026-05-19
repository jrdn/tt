package cmd

import (
	"fmt"

	"github.com/fatih/color"
	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

func newSearchCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "search <query>",
		Aliases: []string{"find"},
		Short:   "Fuzzy-search tasks by title, description, or comment",
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
			header := color.New(color.Faint)
			header.Printf("%-6s  %s  %-13s  %s\n", "ID", "P", "STATUS", "TITLE")
			for _, t := range tasks {
				assignee := ""
				if t.Assignee != nil {
					assignee = color.New(color.FgHiBlack).Sprint("  @" + *t.Assignee)
				}

				parent := ""
				if t.ParentID != nil {
					parent = color.New(color.FgHiBlack).Sprint("  ↳ " + *t.ParentID)
				}

				p := t.Priority
				if p < 0 || p > 3 {
					p = 2
				}
				priLabel := priorityColors[p].Sprint(priorityLabels[p])

				sc, ok := statusColors[t.Status]
				if !ok {
					sc = color.New(color.Reset)
				}
				statusStr := sc.Sprintf("%-13s", string(t.Status))

				fmt.Printf("%-6s  %s  %s  %s%s%s\n",
					t.ID,
					priLabel,
					statusStr,
					t.Title,
					assignee,
					parent,
				)
			}
			return nil
		},
	}
}
