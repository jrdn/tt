package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/jrdn/tt/internal/config"
	ttdb "github.com/jrdn/tt/internal/db"
	ttsync "github.com/jrdn/tt/internal/sync"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "sync",
		Short:       "Sync database with a remote backend",
		Annotations: map[string]string{localOnly: "1"},
		Long: `Push or pull the tt database to/from a configured sync backend.

Supported backends:
  directory  - Copy to/from a local directory (e.g. Dropbox, iCloud)
  scp        - Copy to/from a remote host via SCP
  s3         - Upload/download from an S3 bucket
  git_lfs    - Push/pull via a dedicated git branch with git-lfs

Before pushing, the WAL is checkpointed to ensure a consistent snapshot.
Before pulling, the database is closed, the file is replaced, and the
new database is reopened.`,
	}

	cmd.AddCommand(
		newSyncPushCmd(),
		newSyncPullCmd(),
		newSyncConfigureCmd(),
	)

	return cmd
}

func newSyncPushCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "push",
		Short: "Push local database to the configured sync backend",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			dbName, err := ttdb.RepoDBName()
			if err != nil {
				return err
			}

			syncCfg, err := cfg.GetSyncConfig(dbName)
			if err != nil {
				return fmt.Errorf("%w\nRun 'tt sync configure' to set up a sync backend", err)
			}

			dbPath, err := ttdb.CurrentDBPath()
			if err != nil {
				return err
			}

			// Checkpoint WAL to merge pending frames into the main file,
			// ensuring a consistent snapshot for the transfer.
			if err := ttdb.Checkpoint(db); err != nil {
				return err
			}

			backend, err := ttsync.NewBackend(syncCfg)
			if err != nil {
				return err
			}

			ctx := context.Background()
			if err := backend.Push(ctx, dbPath); err != nil {
				return err
			}

			// Record the current mutation_count as the new baseline so
			// future pulls can detect divergence.
			if count, err := ttdb.GetMeta(db, "mutation_count"); err == nil {
				ttdb.SetMeta(db, "sync_baseline", count)
			}

			if jsonOutput {
				return printJSON(map[string]string{
					"status": "pushed",
					"type":   string(syncCfg.Type),
				})
			}
			fmt.Printf("pushed to %s backend\n", syncCfg.Type)
			return nil
		},
	}
}

func newSyncPullCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Pull database from the configured sync backend",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			dbName, err := ttdb.RepoDBName()
			if err != nil {
				return err
			}

			syncCfg, err := cfg.GetSyncConfig(dbName)
			if err != nil {
				return fmt.Errorf("%w\nRun 'tt sync configure' to set up a sync backend", err)
			}

			dbPath, err := ttdb.CurrentDBPath()
			if err != nil {
				return err
			}

			backend, err := ttsync.NewBackend(syncCfg)
			if err != nil {
				return err
			}

			ctx := context.Background()

			// Pull into a temp dir that holds a file with the same basename
			// as the real DB — backends derive the remote filename from
			// filepath.Base(localPath), so the name must match.
			stagingDir, err := os.MkdirTemp(filepath.Dir(dbPath), ".tt-pull-*")
			if err != nil {
				return fmt.Errorf("create staging dir: %w", err)
			}
			tmpPath := filepath.Join(stagingDir, filepath.Base(dbPath))
			defer os.RemoveAll(stagingDir)

			// Download to the temp file
			if err := backend.Pull(ctx, tmpPath); err != nil {
				return err
			}

			if !force {
				if err := checkNotAhead(db, tmpPath); err != nil {
					return err
				}
			}

			// Checkpoint current DB to ensure clean state before swap
			if err := ttdb.Checkpoint(db); err != nil {
				return err
			}

			// Close the current DB so we can replace the file
			if err := db.Close(); err != nil {
				return fmt.Errorf("close db before pull: %w", err)
			}

			// Remove old WAL/SHM files that may belong to the old DB
			for _, ext := range []string{"-wal", "-shm"} {
				os.Remove(dbPath + ext)
			}

			// Swap the new file into place
			if err := os.Rename(tmpPath, dbPath); err != nil {
				// If rename fails (e.g. cross-device), fall back to copy
				if err := copyFile(tmpPath, dbPath); err != nil {
					os.Remove(tmpPath)
					return fmt.Errorf("replace db file: %w", err)
				}
				os.Remove(tmpPath)
			}

			// Reopen the database with schema, triggers, and meta
			newDB, err := ttdb.OpenPath(dbPath)
			if err != nil {
				return fmt.Errorf("reopen db after pull: %w", err)
			}

			// Replace the package-level db reference so the rest of the
			// process (e.g. defer in main) uses the new connection
			db = newDB

			// Record the remote's mutation_count as the new baseline so
			// future pulls can detect divergence from this point forward.
			if count, err := ttdb.GetMeta(db, "mutation_count"); err == nil {
				ttdb.SetMeta(db, "sync_baseline", count)
			}

			if jsonOutput {
				return printJSON(map[string]string{
					"status": "pulled",
					"type":   string(syncCfg.Type),
				})
			}
			fmt.Printf("pulled from %s backend\n", syncCfg.Type)
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Replace local DB even if it has mutations not in the remote")
	return cmd
}

// checkNotAhead opens the staged remote DB and performs a three-way comparison
// using mutation_count and the last sync baseline recorded on this machine.
//
// The baseline is the mutation_count both sides agreed on after the last sync.
// This lets us distinguish "remote is stale" from "both sides diverged".
func checkNotAhead(localDB *sqlx.DB, remotePath string) error {
	remoteDB, err := ttdb.OpenPath(remotePath)
	if err != nil {
		return fmt.Errorf("open remote db for comparison: %w", err)
	}
	defer remoteDB.Close()

	local, err := getMutationMeta(localDB)
	if err != nil {
		return fmt.Errorf("read local meta: %w", err)
	}
	remote, err := getMutationMeta(remoteDB)
	if err != nil {
		return fmt.Errorf("read remote meta: %w", err)
	}

	localAhead := local.count > local.baseline
	remoteAhead := remote.count > local.baseline

	switch {
	case localAhead && remoteAhead:
		return fmt.Errorf(
			"local and remote have both changed since last sync (baseline: %d, local: %d at %s, remote: %d at %s)\n"+
				"Use --force to overwrite local with remote, or push your local changes first",
			local.baseline, local.count, local.at, remote.count, remote.at,
		)
	case localAhead:
		return fmt.Errorf(
			"local DB is ahead of remote (baseline: %d, local: %d at %s, remote: %d at %s)\n"+
				"Push first, or use --force to overwrite local changes",
			local.baseline, local.count, local.at, remote.count, remote.at,
		)
	}
	return nil
}

type mutationMeta struct {
	count    int
	baseline int
	at       string
}

func getMutationMeta(database *sqlx.DB) (mutationMeta, error) {
	countStr, err := ttdb.GetMeta(database, "mutation_count")
	if err != nil {
		return mutationMeta{}, err
	}
	baselineStr, err := ttdb.GetMeta(database, "sync_baseline")
	if err != nil {
		return mutationMeta{}, err
	}
	at, _ := ttdb.GetMeta(database, "last_mutation_at")

	count, _ := strconv.Atoi(countStr)
	baseline, _ := strconv.Atoi(baselineStr)
	return mutationMeta{count: count, baseline: baseline, at: at}, nil
}

func gitRemoteURL(remote string) (string, error) {
	out, err := exec.Command("git", "remote", "get-url", remote).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

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

func newSyncConfigureCmd() *cobra.Command {
	var (
		backendType string
		path        string
		host        string
		user        string
		port        int
		remotePath  string
		bucket      string
		region      string
		accessKey   string
		secretKey   string
		endpoint    string
		branch      string
		remote      string
		repoURL     string
		unset       bool
	)

	cmd := &cobra.Command{
		Use:   "configure",
		Short: "Configure a sync backend for the current database",
		Long: `Configure a sync backend for the current database.

Examples:
  tt sync configure --type directory --path ~/Dropbox/tt
  tt sync configure --type scp --host myserver.com --user jrdn --remote-path /home/jrdn/tt/tt.db
  tt sync configure --type s3 --bucket my-tt-backup --region us-east-1
  tt sync configure --type git_lfs --branch tt-sync --repo-url git@github.com:you/repo.git`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			dbName, err := ttdb.RepoDBName()
			if err != nil {
				return err
			}

			if unset {
				cfg.UnsetSyncConfig(dbName)
				if err := cfg.Save(); err != nil {
					return err
				}
				if jsonOutput {
					return printJSON(map[string]string{
						"status": "unset",
						"db":     dbName,
					})
				}
				fmt.Printf("sync backend unset for database %q\n", dbName)
				return nil
			}

			if backendType == "" {
				return fmt.Errorf("--type is required (directory, scp, s3, or git_lfs)")
			}

			// For git_lfs: if no --repo-url given, auto-detect from the
			// current repo's remote so sync works outside this directory.
			if config.SyncBackendType(backendType) == config.BackendGitLFS && repoURL == "" {
				if url, err := gitRemoteURL(remote); err == nil {
					repoURL = url
				}
			}

			syncCfg := &config.SyncConfig{
				Type:       config.SyncBackendType(backendType),
				Path:       path,
				Host:       host,
				User:       user,
				Port:       port,
				RemotePath: remotePath,
				Bucket:     bucket,
				Region:     region,
				AccessKey:  accessKey,
				SecretKey:  secretKey,
				Endpoint:   endpoint,
				Branch:     branch,
				Remote:     remote,
				RepoURL:    repoURL,
			}

			if err := syncCfg.Validate(); err != nil {
				return err
			}

			cfg.SetSyncConfig(dbName, syncCfg)
			if err := cfg.Save(); err != nil {
				return err
			}

			if jsonOutput {
				return printJSON(map[string]string{
					"status": "configured",
					"db":     dbName,
					"type":   string(syncCfg.Type),
				})
			}
			fmt.Printf("configured %s sync backend for database %q\n", syncCfg.Type, dbName)
			return nil
		},
	}

	cmd.Flags().StringVar(&backendType, "type", "", "Backend type: directory, scp, s3, git_lfs")
	cmd.Flags().StringVar(&path, "path", "", "Local directory path (directory backend)")
	cmd.Flags().StringVar(&host, "host", "", "Remote host (scp backend)")
	cmd.Flags().StringVar(&user, "user", "", "Remote user (scp backend)")
	cmd.Flags().IntVar(&port, "port", 22, "SSH port (scp backend)")
	cmd.Flags().StringVar(&remotePath, "remote-path", "", "Remote file path (scp backend)")
	cmd.Flags().StringVar(&bucket, "bucket", "", "S3 bucket name (s3 backend)")
	cmd.Flags().StringVar(&region, "region", "", "AWS region (s3 backend)")
	cmd.Flags().StringVar(&accessKey, "access-key", "", "AWS access key (s3 backend)")
	cmd.Flags().StringVar(&secretKey, "secret-key", "", "AWS secret key (s3 backend)")
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "S3-compatible endpoint URL (s3 backend)")
	cmd.Flags().StringVar(&branch, "branch", "tt-sync", "Git branch for sync (git_lfs backend)")
	cmd.Flags().StringVar(&remote, "remote", "origin", "Git remote name (git_lfs backend)")
	cmd.Flags().StringVar(&repoURL, "repo-url", "", "Explicit remote URL (git_lfs backend); auto-detected from --remote if omitted")
	cmd.Flags().BoolVar(&unset, "unset", false, "Remove sync configuration")

	return cmd
}
