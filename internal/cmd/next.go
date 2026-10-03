package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

func newNextCmd() *cobra.Command {
	var as string

	cmd := &cobra.Command{
		Use:   "next",
		Short: "Claim and display the next ready task assigned to you",
		Long: `Atomically find and claim the highest-priority ready task assigned to your handle.

Exits with status 1 when the queue is empty or all matching tasks are blocked.

Use this as the entry point for an agent work loop:

  while tt next --as <handle> --json; do
    # work on the task, then:
    tt update <id> --status=in_review
  done`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			handle := as
			if handle == "" {
				handle = gitConfigHandle()
			}
			if handle == "" {
				return fmt.Errorf("specify --as <handle> or set: git config tt.handle <handle>")
			}

			t, err := store.Next(cmd.Context(), handle)
			if err != nil {
				return err
			}
			if t == nil {
				fmt.Fprintf(os.Stderr, "no ready tasks for %s\n", handle)
				os.Exit(1)
			}

			subtasks, _ := store.List(cmd.Context(), task.ListOpts{ParentID: t.ID})
			rels, _ := store.GetRelations(cmd.Context(), t.ID)
			comments, _ := store.GetComments(cmd.Context(), t.ID)

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
				return printJSON(struct {
					Task      *task.Task      `json:"task"`
					Subtasks  []task.Task     `json:"subtasks"`
					Relations []task.Relation `json:"relations"`
					Comments  []task.Comment  `json:"comments"`
				}{t, subtasks, rels, comments})
			}

			fmt.Printf("ID:      %s\n", t.ID)
			fmt.Printf("Title:   %s\n", t.Title)
			fmt.Printf("Status:  %s\n", t.Status)
			fmt.Printf("Priority:%d\n", t.Priority)
			if t.Assignee != nil {
				fmt.Printf("Assignee:%s\n", *t.Assignee)
			}
			if t.DueDate != nil {
				fmt.Printf("Due:     %s\n", *t.DueDate)
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

	cmd.Flags().StringVar(&as, "as", "", "Handle to claim as (e.g. claude/sonnet-4.6); falls back to git config tt.handle")
	return cmd
}
