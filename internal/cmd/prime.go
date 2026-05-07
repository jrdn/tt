package cmd

import (
	"fmt"
	"strings"

	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

const ttOverview = `tt — local-first task manager

  tt ls                   open + in_progress tasks (default view)
  tt ls --ready           unblocked open tasks (no pending blockers)
  tt ls --status=backlog  backlog tasks
  tt add "title"          create a task (no args opens $EDITOR)
  tt show <id>            full detail: description, subtasks, relations, comments
  tt claim <id>           set in_progress + assign to yourself
  tt done <id> [id...]    mark one or more tasks done
  tt update <id> [id...]  update fields (--status, --assignee, --priority, ...)
  tt comment <id> "note"  append a progress note (use --author to identify yourself)
  tt search <query>       search title, description, and comments
  tt relate <id> blocks <id>  link tasks (blocks | duplicates | related)
  tt edit <id>            edit task in $EDITOR

  IDs are 7-char base62; prefix matching is supported (tt show ab matches abcdefg).
  Priority: 0=critical 1=high 2=normal 3=low. Status: backlog→open→in_progress→done/cancelled.
  All commands accept --json for machine-readable output.`

func newPrimeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prime",
		Short: "Print orientation context: overview, in-progress tasks, and ready work",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println(ttOverview)

			inProgress, err := task.List(db, task.ListOpts{Status: "in_progress"})
			if err != nil {
				return err
			}

			fmt.Println("\n" + rule("In Progress"))
			if len(inProgress) == 0 {
				fmt.Println("  (none)")
			}
			for _, t := range inProgress {
				assignee := ""
				if t.Assignee != nil {
					assignee = "  @" + *t.Assignee
				}
				fmt.Printf("\n  %s  %s%s\n", t.ID, t.Title, assignee)
				if t.Description != nil && *t.Description != "" {
					fmt.Printf("  %s\n", *t.Description)
				}
				comments, _ := task.GetComments(db, t.ID)
				last := comments
				if len(last) > 2 {
					last = last[len(last)-2:]
				}
				for _, c := range last {
					author := "—"
					if c.Author != nil {
						author = *c.Author
					}
					fmt.Printf("    [%s] %s: %s\n", c.CreatedAt[:10], author, c.Body)
				}
			}

			ready, err := task.List(db, task.ListOpts{Ready: true})
			if err != nil {
				return err
			}

			fmt.Printf("\n%s\n", rule(fmt.Sprintf("Ready (%d)", len(ready))))
			if len(ready) == 0 {
				fmt.Println("  (none)")
			}
			for _, t := range ready {
				assignee := ""
				if t.Assignee != nil {
					assignee = "  @" + *t.Assignee
				}
				fmt.Printf("  %s  %s%s\n", t.ID, t.Title, assignee)
			}

			return nil
		},
	}
}

func rule(label string) string {
	const width = 50
	prefix := "── " + label + " "
	dashes := width - len(prefix)
	if dashes < 2 {
		dashes = 2
	}
	return prefix + strings.Repeat("─", dashes)
}
