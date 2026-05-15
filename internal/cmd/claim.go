package cmd

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

func newClaimCmd() *cobra.Command {
	var as string

	cmd := &cobra.Command{
		Use:   "claim <id>",
		Short: "Set a task in_progress and assign it to yourself",
		Long: `Set a task in_progress and assign it to yourself.

The ID can be a plain task ID or a qualified ID (<db_name>.<task_id>).`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			qt, err := resolveTask(args[0])
			if err != nil {
				return err
			}
			defer qt.Close()

			handle := as
			if handle == "" {
				handle = gitConfigHandle()
			}
			if handle == "" {
				return fmt.Errorf("specify --as <handle> or set git config tt.handle")
			}

			status := task.StatusInProgress
			t, err := task.Update(qt.database, qt.id, task.UpdateOpts{
				Status:   &status,
				Assignee: &handle,
			})
			if err != nil {
				return err
			}
			if jsonOutput {
				return printJSON(t)
			}
			fmt.Printf("%s claimed by %s\n", t.ID, handle)
			return nil
		},
	}

	cmd.Flags().StringVar(&as, "as", "", "Handle to assign (e.g. jrdn, claude/opus4.7); falls back to git config tt.handle")
	return cmd
}

func gitConfigHandle() string {
	out, err := exec.Command("git", "config", "tt.handle").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
