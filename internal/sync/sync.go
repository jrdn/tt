package sync

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jrdn/tt/internal/config"
)

// Backend defines the interface for a sync backend.
type Backend interface {
	Push(ctx context.Context, localPath string) error
	Pull(ctx context.Context, localPath string) error
}

// NewBackend creates a sync backend based on the config.
func NewBackend(cfg *config.SyncConfig) (Backend, error) {
	switch cfg.Type {
	case config.BackendDirectory:
		return NewDirectoryBackend(cfg), nil
	case config.BackendSCP:
		return NewSCPBackend(cfg), nil
	case config.BackendS3:
		return NewS3Backend(cfg), nil
	case config.BackendGitLFS:
		return NewGitLFSBackend(cfg), nil
	default:
		return nil, fmt.Errorf("unknown sync backend type: %q", cfg.Type)
	}
}

// DirectoryBackend copies the database file to/from a local directory.
type DirectoryBackend struct {
	path string
}

func NewDirectoryBackend(cfg *config.SyncConfig) *DirectoryBackend {
	return &DirectoryBackend{path: cfg.Path}
}

func (b *DirectoryBackend) Push(ctx context.Context, localPath string) error {
	if err := os.MkdirAll(b.path, 0755); err != nil {
		return fmt.Errorf("create sync directory: %w", err)
	}
	dest := filepath.Join(b.path, filepath.Base(localPath))
	destWal := dest + "-wal"
	destShm := dest + "-shm"

	if err := copyFile(localPath, dest); err != nil {
		return fmt.Errorf("copy db: %w", err)
	}
	// Also copy WAL and SHM files if they exist
	copyIfExists(localPath+"-wal", destWal)
	copyIfExists(localPath+"-shm", destShm)
	return nil
}

func (b *DirectoryBackend) Pull(ctx context.Context, localPath string) error {
	src := filepath.Join(b.path, filepath.Base(localPath))
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return fmt.Errorf("remote db not found at %q", src)
	}
	srcWal := src + "-wal"
	srcShm := src + "-shm"

	if err := copyFile(src, localPath); err != nil {
		return fmt.Errorf("copy db: %w", err)
	}
	copyIfExists(srcWal, localPath+"-wal")
	copyIfExists(srcShm, localPath+"-shm")
	return nil
}

// SCPBackend copies the database file to/from a remote host via scp.
type SCPBackend struct {
	host       string
	user       string
	port       int
	remotePath string
}

func NewSCPBackend(cfg *config.SyncConfig) *SCPBackend {
	port := cfg.Port
	if port == 0 {
		port = 22
	}
	return &SCPBackend{
		host:       cfg.Host,
		user:       cfg.User,
		port:       port,
		remotePath: cfg.RemotePath,
	}
}

func (b *SCPBackend) remoteAddr() string {
	addr := b.host
	if b.user != "" {
		addr = b.user + "@" + b.host
	}
	return addr
}

func (b *SCPBackend) Push(ctx context.Context, localPath string) error {
	remoteDest := b.remoteAddr() + ":" + b.remotePath
	args := []string{"-P", fmt.Sprintf("%d", b.port), localPath, remoteDest}
	cmd := exec.CommandContext(ctx, "scp", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("scp push failed: %w\n%s", err, string(out))
	}
	return nil
}

func (b *SCPBackend) Pull(ctx context.Context, localPath string) error {
	remoteSrc := b.remoteAddr() + ":" + b.remotePath
	args := []string{"-P", fmt.Sprintf("%d", b.port), remoteSrc, localPath}
	cmd := exec.CommandContext(ctx, "scp", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("scp pull failed: %w\n%s", err, string(out))
	}
	return nil
}

// S3Backend uploads/downloads the database file to/from an S3 bucket.
// Uses the AWS CLI.
type S3Backend struct {
	bucket    string
	region    string
	accessKey string
	secretKey string
	endpoint  string
}

func NewS3Backend(cfg *config.SyncConfig) *S3Backend {
	return &S3Backend{
		bucket:    cfg.Bucket,
		region:    cfg.Region,
		accessKey: cfg.AccessKey,
		secretKey: cfg.SecretKey,
		endpoint:  cfg.Endpoint,
	}
}

func (b *S3Backend) s3Path(filename string) string {
	return "s3://" + b.bucket + "/" + filepath.Base(filename)
}

func (b *S3Backend) awsArgs() []string {
	args := []string{"--region", b.region}
	if b.endpoint != "" {
		args = append(args, "--endpoint-url", b.endpoint)
	}
	return args
}

func (b *S3Backend) Push(ctx context.Context, localPath string) error {
	args := append(b.awsArgs(), "s3", "cp", localPath, b.s3Path(localPath))
	cmd := exec.CommandContext(ctx, "aws", args...)
	cmd.Env = b.envWithCreds(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("aws s3 cp push failed: %w\n%s", err, string(out))
	}
	return nil
}

func (b *S3Backend) Pull(ctx context.Context, localPath string) error {
	args := append(b.awsArgs(), "s3", "cp", b.s3Path(localPath), localPath)
	cmd := exec.CommandContext(ctx, "aws", args...)
	cmd.Env = b.envWithCreds(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("aws s3 cp pull failed: %w\n%s", err, string(out))
	}
	return nil
}

func (b *S3Backend) envWithCreds(env []string) []string {
	if b.accessKey == "" || b.secretKey == "" {
		return env
	}
	var result []string
	for _, e := range env {
		if strings.HasPrefix(e, "AWS_ACCESS_KEY_ID=") || strings.HasPrefix(e, "AWS_SECRET_ACCESS_KEY=") || strings.HasPrefix(e, "AWS_SESSION_TOKEN=") {
			continue
		}
		result = append(result, e)
	}
	result = append(result, "AWS_ACCESS_KEY_ID="+b.accessKey)
	result = append(result, "AWS_SECRET_ACCESS_KEY="+b.secretKey)
	return result
}

// GitLFSBackend pushes/pulls the database file using git and git-lfs
// on a separate branch.
//
// When repoURL is set, all git operations happen inside a dedicated local
// repo at <dbdir>/.sync-<dbname>/, making sync CWD-independent. When
// repoURL is empty it falls back to the CWD git repo + remote name.
type GitLFSBackend struct {
	branch  string
	remote  string // remote name, used when repoURL is empty
	repoURL string // explicit remote URL; enables CWD-independent sync
}

func NewGitLFSBackend(cfg *config.SyncConfig) *GitLFSBackend {
	branch := cfg.Branch
	if branch == "" {
		branch = "tt-sync"
	}
	remote := cfg.Remote
	if remote == "" {
		remote = "origin"
	}
	return &GitLFSBackend{branch: branch, remote: remote, repoURL: cfg.RepoURL}
}

func (b *GitLFSBackend) Push(ctx context.Context, localPath string) error {
	if _, err := exec.LookPath("git-lfs"); err != nil {
		return fmt.Errorf("git-lfs is required but not installed: %w", err)
	}
	if b.repoURL != "" {
		return b.pushURL(ctx, localPath)
	}
	return b.pushCWD(ctx, localPath)
}

func (b *GitLFSBackend) Pull(ctx context.Context, localPath string) error {
	if b.repoURL != "" {
		return b.pullURL(ctx, localPath)
	}
	return b.pullCWD(ctx, localPath)
}

// syncRepoPath returns the path of the dedicated local sync repo for a given
// DB file. It lives alongside the DB so it's always accessible regardless of CWD.
func (b *GitLFSBackend) syncRepoPath(localPath string) string {
	return filepath.Join(filepath.Dir(localPath), ".sync-"+filepath.Base(localPath))
}

// ensureSyncRepo initialises the dedicated sync repo if it doesn't exist and
// ensures its remote URL is current.
func (b *GitLFSBackend) ensureSyncRepo(ctx context.Context, syncRepo string) error {
	if _, err := os.Stat(filepath.Join(syncRepo, ".git")); os.IsNotExist(err) {
		if err := os.MkdirAll(syncRepo, 0700); err != nil {
			return fmt.Errorf("create sync repo: %w", err)
		}
		if err := runCmd(ctx, syncRepo, "git", "init"); err != nil {
			return err
		}
		if err := runCmd(ctx, syncRepo, "git", "config", "user.email", "tt-sync@local"); err != nil {
			return err
		}
		if err := runCmd(ctx, syncRepo, "git", "config", "user.name", "tt sync"); err != nil {
			return err
		}
		if err := runCmd(ctx, syncRepo, "git", "lfs", "install", "--local"); err != nil {
			return err
		}
		if err := runCmd(ctx, syncRepo, "git", "remote", "add", "origin", b.repoURL); err != nil {
			return err
		}
	} else {
		// Update remote URL in case it changed.
		runCmd(ctx, syncRepo, "git", "remote", "set-url", "origin", b.repoURL)
	}
	return nil
}

func (b *GitLFSBackend) pushURL(ctx context.Context, localPath string) error {
	syncRepo := b.syncRepoPath(localPath)
	if err := b.ensureSyncRepo(ctx, syncRepo); err != nil {
		return err
	}

	filename := filepath.Base(localPath)

	// Fetch remote branch; ignore error if it doesn't exist yet.
	runCmd(ctx, syncRepo, "git", "fetch", "origin", b.branch)

	remoteExists := runCmd(ctx, syncRepo, "git", "rev-parse", "--verify", "origin/"+b.branch) == nil
	if remoteExists {
		// Reset local branch to remote state, creating it if needed.
		if err := runCmd(ctx, syncRepo, "git", "checkout", "-B", b.branch, "origin/"+b.branch); err != nil {
			return err
		}
	} else {
		// First push: create an orphan branch with an initial empty commit
		// so the branch ref exists before we try to push it.
		if err := runCmd(ctx, syncRepo, "git", "checkout", "--orphan", b.branch); err != nil {
			return err
		}
		runCmd(ctx, syncRepo, "git", "rm", "-rf", "--cached", ".")
		if err := runCmd(ctx, syncRepo, "git", "commit", "--allow-empty", "-m", "init sync branch"); err != nil {
			return err
		}
	}

	runCmd(ctx, syncRepo, "git", "lfs", "track", filename)
	if err := copyFile(localPath, filepath.Join(syncRepo, filename)); err != nil {
		return err
	}
	runCmd(ctx, syncRepo, "git", "add", ".gitattributes")
	if err := runCmd(ctx, syncRepo, "git", "add", filename); err != nil {
		return err
	}
	if err := runCmd(ctx, syncRepo, "git", "commit", "--allow-empty", "-m", "tt sync push"); err != nil {
		return err
	}
	if err := runCmd(ctx, syncRepo, "git", "push", "--force", "origin", b.branch+":"+b.branch); err != nil {
		return fmt.Errorf("push sync branch: %w", err)
	}
	return nil
}

func (b *GitLFSBackend) pullURL(ctx context.Context, localPath string) error {
	syncRepo := b.syncRepoPath(localPath)
	if err := b.ensureSyncRepo(ctx, syncRepo); err != nil {
		return err
	}

	filename := filepath.Base(localPath)

	if err := runCmd(ctx, syncRepo, "git", "fetch", "origin", b.branch); err != nil {
		return fmt.Errorf("fetch sync branch %q: %w", b.branch, err)
	}

	localExists := runCmd(ctx, syncRepo, "git", "rev-parse", "--verify", b.branch) == nil
	if localExists {
		runCmd(ctx, syncRepo, "git", "checkout", b.branch)
		if err := runCmd(ctx, syncRepo, "git", "reset", "--hard", "origin/"+b.branch); err != nil {
			return err
		}
	} else {
		if err := runCmd(ctx, syncRepo, "git", "checkout", "--track", "origin/"+b.branch); err != nil {
			return err
		}
	}

	runCmd(ctx, syncRepo, "git", "lfs", "pull")

	src := filepath.Join(syncRepo, filename)
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return fmt.Errorf("db file not found in sync branch")
	}
	return copyFile(src, localPath)
}

func (b *GitLFSBackend) pushCWD(ctx context.Context, localPath string) error {
	filename := filepath.Base(localPath)

	rootOut, err := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		return fmt.Errorf("get git root: %w", err)
	}
	gitRoot := strings.TrimSpace(string(rootOut))

	worktreeDir := filepath.Join(gitRoot, ".git", "tt-sync-worktree")
	os.RemoveAll(worktreeDir)

	branchExists := runCmd(ctx, gitRoot, "git", "rev-parse", "--verify", b.branch) == nil
	if branchExists {
		if err := runCmd(ctx, gitRoot, "git", "worktree", "add", "--force", worktreeDir, b.branch); err != nil {
			return err
		}
	} else {
		if err := runCmd(ctx, gitRoot, "git", "worktree", "add", "--force", "--orphan", "-b", b.branch, worktreeDir); err != nil {
			return err
		}
	}
	defer runCmd(ctx, gitRoot, "git", "worktree", "remove", "--force", worktreeDir)

	runCmd(ctx, worktreeDir, "git", "lfs", "install", "--local")
	runCmd(ctx, worktreeDir, "git", "lfs", "track", filename)
	runCmd(ctx, worktreeDir, "git", "add", ".gitattributes")

	if err := copyFile(localPath, filepath.Join(worktreeDir, filename)); err != nil {
		return err
	}
	if err := runCmd(ctx, worktreeDir, "git", "add", filename); err != nil {
		return err
	}
	if err := runCmd(ctx, worktreeDir, "git", "commit", "--allow-empty", "-m", "tt sync push"); err != nil {
		return err
	}
	if err := runCmd(ctx, gitRoot, "git", "push", "--force", b.remote, b.branch+":"+b.branch); err != nil {
		return fmt.Errorf("push sync branch to %s: %w", b.remote, err)
	}
	return nil
}

func (b *GitLFSBackend) pullCWD(ctx context.Context, localPath string) error {
	filename := filepath.Base(localPath)
	rootOut, err := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		return fmt.Errorf("not in a git repo: %w", err)
	}
	gitRoot := strings.TrimSpace(string(rootOut))

	if err := runCmd(ctx, gitRoot, "git", "fetch", b.remote, b.branch); err != nil {
		return fmt.Errorf("fetch sync branch %q from %q: %w", b.branch, b.remote, err)
	}

	worktreeDir := filepath.Join(os.TempDir(), fmt.Sprintf("tt-pull-%d", os.Getpid()))
	defer os.RemoveAll(worktreeDir)

	remoteRef := b.remote + "/" + b.branch
	if err := runCmd(ctx, gitRoot, "git", "worktree", "add", "--force", worktreeDir, remoteRef); err != nil {
		return err
	}
	defer runCmd(ctx, gitRoot, "git", "worktree", "remove", "--force", worktreeDir)

	runCmd(ctx, worktreeDir, "git", "lfs", "pull")

	src := filepath.Join(worktreeDir, filename)
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return fmt.Errorf("db file not found in sync branch")
	}
	return copyFile(src, localPath)
}

// Helper functions

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func copyIfExists(src, dst string) error {
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil
	}
	return copyFile(src, dst)
}

func runCmd(ctx context.Context, dir, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %v: %w\n%s", name, args, err, string(out))
	}
	return nil
}
