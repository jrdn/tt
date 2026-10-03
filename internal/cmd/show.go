package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/jrdn/tt/internal/task"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

func renderMarkdown(s string) string {
	if !isatty.IsTerminal(os.Stdout.Fd()) {
		return s
	}
	r, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(100))
	if err != nil {
		return s
	}
	out, err := r.Render(s)
	if err != nil {
		return s
	}
	return out
}

func newShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "show <id>",
		Aliases: []string{"s"},
		Short:   "Show full task details",
		Long: `Show full task details.

The ID can be a plain task ID (resolved in the current repo's database)
or a qualified ID in the form <db_name>.<task_id> to look up in a
specific database (e.g. "myrepo.a3B7xK").`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			qt, err := resolveTask(args[0])
			if err != nil {
				return err
			}
			defer qt.Close()

			t, err := qt.store.Get(cmd.Context(), qt.id)
			if err != nil {
				return err
			}

			subtasks, _ := qt.store.List(cmd.Context(), task.ListOpts{ParentID: t.ID})
			rels, _ := qt.store.GetRelations(cmd.Context(), t.ID)
			comments, _ := qt.store.GetComments(cmd.Context(), t.ID)
			extRefs, _ := qt.store.GetExternalRefs(cmd.Context(), t.ID)

			if jsonOutput {
				// Empty lists print as [], not null, in both modes.
				if subtasks == nil {
					subtasks = []task.Task{}
				}
				if rels == nil {
					rels = []task.Relation{}
				}
				if comments == nil {
					comments = []task.Comment{}
				}
				if extRefs == nil {
					extRefs = []task.ExternalRef{}
				}
				return printJSON(struct {
					Task         *task.Task         `json:"task"`
					Subtasks     []task.Task        `json:"subtasks"`
					Relations    []task.Relation    `json:"relations"`
					Comments     []task.Comment     `json:"comments"`
					ExternalRefs []task.ExternalRef `json:"external_refs"`
				}{t, subtasks, rels, comments, extRefs})
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
				fmt.Print("\n")
				fmt.Print(renderMarkdown(*t.Description))
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

			if len(extRefs) > 0 {
				fmt.Println("\nExternal refs:")
				for _, r := range extRefs {
					if r.URL != nil && *r.URL != "" {
						fmt.Printf("  %s:%s  %s\n", r.Source, r.ExternalID, *r.URL)
					} else {
						fmt.Printf("  %s:%s\n", r.Source, r.ExternalID)
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
					fmt.Printf("  [%s] [%s] %s:\n", c.ID, c.CreatedAt[:10], author)
					rendered := renderMarkdown(c.Body)
					for _, line := range strings.Split(strings.TrimRight(rendered, "\n"), "\n") {
						fmt.Printf("  %s\n", line)
					}
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
