package cmd

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWorktreeTarget_SiblingDefault(t *testing.T) {
	t.Setenv("GIT_WORKTREE_ROOT", "")
	root := "/Users/jrdn/ws/tt"
	branch, path := worktreeTarget(root, "abc1234")
	if branch != "tt/abc1234" {
		t.Errorf("branch = %q, want %q", branch, "tt/abc1234")
	}
	want := "/Users/jrdn/ws/tt-abc1234"
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}

func TestWorktreeTarget_EnvOverride(t *testing.T) {
	t.Setenv("GIT_WORKTREE_ROOT", "/Users/jrdn/worktrees")
	root := "/Users/jrdn/ws/tt"
	branch, path := worktreeTarget(root, "abc1234")
	if branch != "tt/abc1234" {
		t.Errorf("branch = %q, want %q", branch, "tt/abc1234")
	}
	want := "/Users/jrdn/worktrees/tt-abc1234"
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}

// initTestRepo creates a temp git repo with one commit and returns its root.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	run("commit", "--allow-empty", "-q", "-m", "init")
	return dir
}

func TestBranchExists(t *testing.T) {
	root := initTestRepo(t)

	if branchExists(root, "tt/nope") {
		t.Error("expected false for nonexistent branch")
	}

	cmd := exec.Command("git", "-C", root, "branch", "tt/abc1234")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git branch: %v\n%s", err, out)
	}

	if !branchExists(root, "tt/abc1234") {
		t.Error("expected true for existing branch")
	}
}

func TestRunGit_Success(t *testing.T) {
	root := initTestRepo(t)
	if err := runGit(root, "rev-parse", "HEAD"); err != nil {
		t.Errorf("runGit: %v", err)
	}
}

func TestRunGit_Failure(t *testing.T) {
	root := initTestRepo(t)
	err := runGit(root, "not-a-real-git-command")
	if err == nil {
		t.Fatal("expected error for invalid git subcommand")
	}
}

func TestWorktreeTarget_CreatesRealWorktree(t *testing.T) {
	root := initTestRepo(t)
	worktreeRoot := t.TempDir()
	t.Setenv("GIT_WORKTREE_ROOT", worktreeRoot)

	branch, path := worktreeTarget(root, "abc1234")
	wantPath := filepath.Join(worktreeRoot, filepath.Base(root)+"-abc1234")
	if path != wantPath {
		t.Fatalf("path = %q, want %q", path, wantPath)
	}

	if err := runGit(root, "worktree", "add", "-b", branch, path); err != nil {
		t.Fatalf("worktree add: %v", err)
	}
	if !branchExists(root, branch) {
		t.Error("expected branch to exist after worktree add")
	}

	if err := runGit(root, "worktree", "remove", "--force", path); err != nil {
		t.Fatalf("worktree remove: %v", err)
	}
}
