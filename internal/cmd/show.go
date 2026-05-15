package cmd

import (
	"fmt"
	"strings"

	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

func newShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "show <id>",
		Aliases: []string{"s"},
		Short:   "Show full task details",
		Long: `Show full task details.

The ID can be a plain task ID (resolved in the current repo's database)
or a qualified ID in the form <db_name>.<task_id> to look up in a
specific database (e.g. "myrepo.a3B7xK").`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			qt, err := resolveTask(args[0])
			if err != nil {
				return err
			}
			defer qt.Close()

			t, err := task.Get(qt.database, qt.id)
			if err != nil {
				return err
			}

			subtasks, _ := task.List(qt.database, task.ListOpts{ParentID: t.ID})
			rels, _ := task.GetRelations(qt.database, t.ID)
			comments, _ := task.GetComments(qt.database, t.ID)

			if jsonOutput {
				return printJSON(struct {
					Task      *task.Task       `json:"task"`
					Subtasks  []task.Task      `json:"subtasks"`
					Relations []task.Relation  `json:"relations"`
					Comments  []task.Comment   `json:"comments"`
				}{t, subtasks, rels, comments})
			}

			fmt.Printf("ID:      %s\n", t.ID)
			fmt.Printf("Title:   %s\n", t.Title)
			fmt.Printf("Status:  %s\n", t.Status)
			fmt.Printf("Priority:%d\n", t.Priority)
			if t.ParentID != nil {
				fmt.Printf("Parent:  %s\n", *t.ParentID)
			}
			if t.Assignee != nil {
				fmt.Printf("Assignee:%s\n", *t.Assignee)
			}
			if t.DueDate != nil {
				fmt.Printf("Due:     %s\n", *t.DueDate)
			}
			if t.CreatedBy != nil {
				fmt.Printf("Created by: %s\n", *t.CreatedBy)
			}
			fmt.Printf("Created: %s\n", t.CreatedAt)
			fmt.Printf("Updated: %s\n", t.UpdatedAt)
			if t.ClosedAt != nil {
				fmt.Printf("Closed:  %s\n", *t.ClosedAt)
			}
			if t.Description != nil && *t.Description != "" {
				fmt.Printf("\n%s\n", *t.Description)
			}

			if len(subtasks) > 0 {
				fmt.Println("\nSubtasks:")
				for _, s := range subtasks {
					fmt.Printf("  %-6s  %-12s  %s\n", s.ID, s.Status, s.Title)
				}
			}

			if len(rels) > 0 {
				fmt.Println("\nRelations:")
				for _, r := range rels {
					if r.FromID == t.ID {
						fmt.Printf("  %s → %s %s\n", t.ID, r.Type, r.ToID)
					} else {
						fmt.Printf("  %s ← %s %s\n", r.FromID, inverseRelation(r.Type), t.ID)
					}
				}
			}

			if len(comments) > 0 {
				fmt.Println("\nComments:")
				for _, c := range comments {
					author := "—"
					if c.Author != nil {
						author = *c.Author
					}
					fmt.Printf("  [%s] [%s] %s: %s\n", c.ID, c.CreatedAt[:10], author, c.Body)
				}
			}

			return nil
		},
	}
}

func inverseRelation(relType string) string {
	switch strings.ToLower(relType) {
	case "blocks":
		return "blocked by"
	case "duplicates":
		return "duplicated by"
	default:
		return relType
	}
}
