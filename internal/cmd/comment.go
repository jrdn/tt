package cmd

import (
	"fmt"
	"os/user"

	"github.com/spf13/cobra"
)

func newCommentCmd() *cobra.Command {
	var author string

	cmd := &cobra.Command{
		Use:     "comment <id> <body>",
		Aliases: []string{"c"},
		Short:   "Add a comment to a task",
		Long: `Add a comment to a task.

The ID can be a plain task ID or a qualified ID (<db_name>.<task_id>).`,
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			qt, err := resolveTask(args[0])
			if err != nil {
				return err
			}
			defer qt.Close()

			if author == "" {
				if u, err := user.Current(); err == nil {
					author = u.Username
				}
			}
			var authorPtr *string
			if author != "" {
				authorPtr = &author
			}
			c, err := qt.store.AddComment(cmd.Context(), qt.id, args[1], authorPtr)
			if err != nil {
				return err
			}
			if jsonOutput {
				return printJSON(c)
			}
			fmt.Printf("comment %s added\n", c.ID)
			return nil
		},
	}

	cmd.Flags().StringVar(&author, "author", "", "Author handle")
	return cmd
}
