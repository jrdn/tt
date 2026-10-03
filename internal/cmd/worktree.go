package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newWorktreeCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "worktree <id>",
		Aliases: []string{"wt"},
		Short:   "Create a git branch and worktree for a task",
		Long: `Create a git branch and worktree for a task.

The worktree is created under $GIT_WORKTREE_ROOT if that env var is set,
otherwise as a sibling of the repo root (../<repo>-<id>). The branch is
named tt/<id>, and is reused if it already exists.`,
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

			root := gitRoot()
			if root == "" {
				return fmt.Errorf("not inside a git repository")
			}

			branch, path := worktreeTarget(root, t.ID)

			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("worktree path already exists: %s", path)
			}

			if branchExists(root, branch) {
				if err := runGit(root, "worktree", "add", path, branch); err != nil {
					return err
				}
			} else {
				if err := runGit(root, "worktree", "add", "-b", branch, path); err != nil {
					return err
				}
			}

			if jsonOutput {
				return printJSON(struct {
					Task   string `json:"task"`
					Branch string `json:"branch"`
					Path   string `json:"path"`
				}{t.ID, branch, path})
			}
			fmt.Printf("created worktree for %s\n  branch: %s\n  path:   %s\n", t.ID, branch, path)
			return nil
		},
	}
}

// worktreeTarget computes the branch name and worktree path for a task,
// respecting GIT_WORKTREE_ROOT if set.
func worktreeTarget(root, id string) (branch, path string) {
	base := os.Getenv("GIT_WORKTREE_ROOT")
	if base == "" {
		base = filepath.Dir(root)
	}
	repoName := filepath.Base(root)
	branch = "tt/" + id
	path = filepath.Join(base, repoName+"-"+id)
	return branch, path
}

func branchExists(root, branch string) bool {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return cmd.Run() == nil
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %w\n%s", args, err, string(out))
	}
	return nil
}
