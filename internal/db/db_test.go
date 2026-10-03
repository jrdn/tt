package db

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRepoDBNameUsesParentRepoForNestedGitRepo(t *testing.T) {
	setIsolatedConfigHome(t)

	parent := filepath.Join(t.TempDir(), "parent-repo")
	nested := filepath.Join(parent, "third_party", "nested-repo")
	mkdirAll(t, nested)
	runGit(t, parent, "init")
	runGit(t, nested, "init")

	chdir(t, nested)

	got, err := repoDBName()
	if err != nil {
		t.Fatalf("repoDBName: %v", err)
	}
	if got != "parent-repo" {
		t.Fatalf("repoDBName() = %q, want %q", got, "parent-repo")
	}
}

func TestRepoDBNameUsesParentRepoConfigForNestedGitRepo(t *testing.T) {
	setIsolatedConfigHome(t)

	parent := filepath.Join(t.TempDir(), "parent-repo")
	nested := filepath.Join(parent, "third_party", "nested-repo")
	mkdirAll(t, nested)
	runGit(t, parent, "init")
	runGit(t, parent, "config", "tt.db-name", "parent-tasks")
	runGit(t, nested, "init")
	runGit(t, nested, "config", "tt.db-name", "nested-tasks")

	chdir(t, nested)

	got, err := repoDBName()
	if err != nil {
		t.Fatalf("repoDBName: %v", err)
	}
	if got != "parent-tasks" {
		t.Fatalf("repoDBName() = %q, want %q", got, "parent-tasks")
	}
}

func TestRepoDBNameUsesPrimaryRepoForLinkedWorktree(t *testing.T) {
	setIsolatedConfigHome(t)

	base := t.TempDir()
	primary := filepath.Join(base, "infra")
	linked := filepath.Join(base, "wt", "infra", "inspiring-buck-2ed1e3")
	mkdirAll(t, primary)
	initRepoWithCommit(t, primary)
	runGit(t, primary, "worktree", "add", "-b", "work", linked)

	chdir(t, linked)

	got, err := repoDBName()
	if err != nil {
		t.Fatalf("repoDBName: %v", err)
	}
	if got != "infra" {
		t.Fatalf("repoDBName() = %q, want %q", got, "infra")
	}
}

func TestRepoDBNameUsesPrimaryRepoConfigForLinkedWorktree(t *testing.T) {
	setIsolatedConfigHome(t)

	base := t.TempDir()
	primary := filepath.Join(base, "infra")
	linked := filepath.Join(base, "wt", "infra", "inspiring-buck-2ed1e3")
	mkdirAll(t, primary)
	initRepoWithCommit(t, primary)
	runGit(t, primary, "config", "tt.db-name", "infra-tasks")
	runGit(t, primary, "worktree", "add", "-b", "work", linked)

	chdir(t, linked)

	got, err := repoDBName()
	if err != nil {
		t.Fatalf("repoDBName: %v", err)
	}
	if got != "infra-tasks" {
		t.Fatalf("repoDBName() = %q, want %q", got, "infra-tasks")
	}
}

func TestRepoDBNameUsesCurrentRepoWhenNoParentRepo(t *testing.T) {
	setIsolatedConfigHome(t)

	repo := filepath.Join(t.TempDir(), "solo-repo")
	mkdirAll(t, repo)
	runGit(t, repo, "init")

	chdir(t, repo)

	got, err := repoDBName()
	if err != nil {
		t.Fatalf("repoDBName: %v", err)
	}
	if got != "solo-repo" {
		t.Fatalf("repoDBName() = %q, want %q", got, "solo-repo")
	}
}

func setIsolatedConfigHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("restore cwd %s: %v", previous, err)
		}
	})
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

func initRepoWithCommit(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	runGit(t, dir, "commit", "--allow-empty", "-m", "init")
}

func TestCheckpoint(t *testing.T) {
	setIsolatedConfigHome(t)

	d, err := OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	defer d.Close()

	_, err = d.Exec(`INSERT INTO tasks (id, title, status, priority, created_at, updated_at)
		VALUES ('checkpoint1', 'test', 'open', 2, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := Checkpoint(d); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&count); err != nil {
		t.Fatalf("count after checkpoint: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 task after checkpoint, got %d", count)
	}
}

func TestDBFilesForCopy(t *testing.T) {
	files := DBFilesForCopy("/tmp/test.db")
	if len(files) != 1 || files[0] != "/tmp/test.db" {
		t.Errorf("expected only main file, got %v", files)
	}
}
