package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jrdn/tt/internal/config"
)

func TestNewBackend(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *config.SyncConfig
		wantErr bool
	}{
		{
			name:    "directory",
			cfg:     &config.SyncConfig{Type: config.BackendDirectory, Path: "/tmp"},
			wantErr: false,
		},
		{
			name:    "scp",
			cfg:     &config.SyncConfig{Type: config.BackendSCP, Host: "h", RemotePath: "/p"},
			wantErr: false,
		},
		{
			name:    "s3",
			cfg:     &config.SyncConfig{Type: config.BackendS3, Bucket: "b", Region: "r"},
			wantErr: false,
		},
		{
			name:    "git_lfs",
			cfg:     &config.SyncConfig{Type: config.BackendGitLFS, Branch: "sync"},
			wantErr: false,
		},
		{
			name:    "unknown",
			cfg:     &config.SyncConfig{Type: "bogus"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := NewBackend(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewBackend() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && b == nil {
				t.Error("expected non-nil backend")
			}
		})
	}
}

func TestDirectoryBackend_PushPull(t *testing.T) {
	syncDir := t.TempDir()
	workDir := t.TempDir()

	// Create a source db file
	srcPath := filepath.Join(workDir, "test.db")
	content := []byte("fake sqlite db content")
	if err := os.WriteFile(srcPath, content, 0644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	// Push
	backend := NewDirectoryBackend(&config.SyncConfig{
		Type: config.BackendDirectory,
		Path: syncDir,
	})

	ctx := context.Background()
	if err := backend.Push(ctx, srcPath); err != nil {
		t.Fatalf("Push: %v", err)
	}

	// Verify file was copied
	pushedPath := filepath.Join(syncDir, "test.db")
	pushed, err := os.ReadFile(pushedPath)
	if err != nil {
		t.Fatalf("read pushed file: %v", err)
	}
	if string(pushed) != string(content) {
		t.Errorf("pushed content = %q, want %q", pushed, content)
	}

	// Modify source to simulate a different state
	if err := os.WriteFile(srcPath, []byte("modified content"), 0644); err != nil {
		t.Fatalf("modify source: %v", err)
	}

	// Pull should restore the pushed version
	if err := backend.Pull(ctx, srcPath); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	restored, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(restored) != string(content) {
		t.Errorf("restored content = %q, want %q", restored, content)
	}
}

func TestDirectoryBackend_Pull_NotFound(t *testing.T) {
	syncDir := t.TempDir()
	workDir := t.TempDir()

	backend := NewDirectoryBackend(&config.SyncConfig{
		Type: config.BackendDirectory,
		Path: syncDir,
	})

	ctx := context.Background()
	srcPath := filepath.Join(workDir, "nonexistent.db")
	err := backend.Pull(ctx, srcPath)
	if err == nil {
		t.Error("expected error when pulling nonexistent db")
	}
}

func TestDirectoryBackend_Push_CreatesDir(t *testing.T) {
	parentDir := t.TempDir()
	syncDir := filepath.Join(parentDir, "nested", "sync", "dir")
	workDir := t.TempDir()

	srcPath := filepath.Join(workDir, "test.db")
	if err := os.WriteFile(srcPath, []byte("data"), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	backend := NewDirectoryBackend(&config.SyncConfig{
		Type: config.BackendDirectory,
		Path: syncDir,
	})

	ctx := context.Background()
	if err := backend.Push(ctx, srcPath); err != nil {
		t.Fatalf("Push: %v", err)
	}

	if _, err := os.Stat(filepath.Join(syncDir, "test.db")); err != nil {
		t.Errorf("expected pushed file to exist: %v", err)
	}
}

func TestDirectoryBackend_PushPreservesFilename(t *testing.T) {
	syncDir := t.TempDir()
	workDir := t.TempDir()

	// Test with different filenames
	filenames := []string{"myrepo.db", "tasks.db", "a-b_c.db"}
	for _, fn := range filenames {
		srcPath := filepath.Join(workDir, fn)
		if err := os.WriteFile(srcPath, []byte(fn+" content"), 0644); err != nil {
			t.Fatalf("write %s: %v", fn, err)
		}

		backend := NewDirectoryBackend(&config.SyncConfig{
			Type: config.BackendDirectory,
			Path: syncDir,
		})

		ctx := context.Background()
		if err := backend.Push(ctx, srcPath); err != nil {
			t.Fatalf("Push %s: %v", fn, err)
		}

		pushedPath := filepath.Join(syncDir, fn)
		data, err := os.ReadFile(pushedPath)
		if err != nil {
			t.Fatalf("read pushed %s: %v", fn, err)
		}
		if string(data) != fn+" content" {
			t.Errorf("%s content mismatch", fn)
		}
	}
}

func TestSCPBackend_RemoteAddr(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.SyncConfig
		want string
	}{
		{
			name: "with user",
			cfg:  &config.SyncConfig{Type: config.BackendSCP, Host: "example.com", User: "deploy"},
			want: "deploy@example.com",
		},
		{
			name: "without user",
			cfg:  &config.SyncConfig{Type: config.BackendSCP, Host: "example.com"},
			want: "example.com",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewSCPBackend(tt.cfg)
			if got := b.remoteAddr(); got != tt.want {
				t.Errorf("remoteAddr() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSCPBackend_DefaultPort(t *testing.T) {
	cfg := &config.SyncConfig{Type: config.BackendSCP, Host: "h", RemotePath: "/p"}
	b := NewSCPBackend(cfg)
	if b.port != 22 {
		t.Errorf("default port = %d, want 22", b.port)
	}

	cfg2 := &config.SyncConfig{Type: config.BackendSCP, Host: "h", RemotePath: "/p", Port: 2222}
	b2 := NewSCPBackend(cfg2)
	if b2.port != 2222 {
		t.Errorf("custom port = %d, want 2222", b2.port)
	}
}

func TestS3Backend_S3Path(t *testing.T) {
	b := NewS3Backend(&config.SyncConfig{
		Type:   config.BackendS3,
		Bucket: "my-bucket",
		Region: "us-east-1",
	})
	got := b.s3Path("/some/path/tasks.db")
	want := "s3://my-bucket/tasks.db"
	if got != want {
		t.Errorf("s3Path() = %q, want %q", got, want)
	}
}

func TestS3Backend_AwsArgs(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     []string
	}{
		{
			name:     "no endpoint",
			endpoint: "",
			want:     []string{"--region", "us-east-1"},
		},
		{
			name:     "with endpoint",
			endpoint: "https://minio.local",
			want:     []string{"--region", "us-east-1", "--endpoint-url", "https://minio.local"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewS3Backend(&config.SyncConfig{
				Type:     config.BackendS3,
				Bucket:   "b",
				Region:   "us-east-1",
				Endpoint: tt.endpoint,
			})
			got := b.awsArgs()
			if len(got) != len(tt.want) {
				t.Errorf("awsArgs() = %v, want %v", got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("awsArgs()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestS3Backend_EnvWithCreds(t *testing.T) {
	b := NewS3Backend(&config.SyncConfig{
		Type:      config.BackendS3,
		Bucket:    "b",
		Region:    "us-east-1",
		AccessKey: "AKIAIOSFODNN7EXAMPLE",
		SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	})

	env := []string{"PATH=/usr/bin", "HOME=/home/user", "AWS_ACCESS_KEY_ID=old", "AWS_SECRET_ACCESS_KEY=old"}
	result := b.envWithCreds(env)

	// Should not contain old creds
	for _, e := range result {
		if e == "AWS_ACCESS_KEY_ID=old" || e == "AWS_SECRET_ACCESS_KEY=old" {
			t.Errorf("old credentials should have been filtered out: %s", e)
		}
	}

	// Should contain new creds
	var hasAccess, hasSecret bool
	for _, e := range result {
		if e == "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE" {
			hasAccess = true
		}
		if e == "AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" {
			hasSecret = true
		}
	}
	if !hasAccess {
		t.Error("new access key not found in env")
	}
	if !hasSecret {
		t.Error("new secret key not found in env")
	}
}

func TestS3Backend_EnvWithCreds_Empty(t *testing.T) {
	b := NewS3Backend(&config.SyncConfig{
		Type:   config.BackendS3,
		Bucket: "b",
		Region: "us-east-1",
	})

	env := []string{"PATH=/usr/bin"}
	result := b.envWithCreds(env)

	if len(result) != len(env) {
		t.Errorf("env should be unchanged when no creds: got %d, want %d", len(result), len(env))
	}
}

func TestGitLFSBackend_DefaultBranch(t *testing.T) {
	b := NewGitLFSBackend(&config.SyncConfig{
		Type: config.BackendGitLFS,
	})
	if b.branch != "tt-sync" {
		t.Errorf("default branch = %q, want %q", b.branch, "tt-sync")
	}

	b2 := NewGitLFSBackend(&config.SyncConfig{
		Type:   config.BackendGitLFS,
		Branch: "custom-branch",
	})
	if b2.branch != "custom-branch" {
		t.Errorf("custom branch = %q, want %q", b2.branch, "custom-branch")
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")

	content := []byte("hello world")
	if err := os.WriteFile(src, content, 0644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile: %v", err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("copied content = %q, want %q", data, content)
	}
}

func TestCopyIfExists_Exists(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")

	if err := os.WriteFile(src, []byte("data"), 0644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	if err := copyIfExists(src, dst); err != nil {
		t.Fatalf("copyIfExists: %v", err)
	}

	data, _ := os.ReadFile(dst)
	if string(data) != "data" {
		t.Errorf("content = %q, want %q", data, "data")
	}
}

func TestCopyIfExists_NotExists(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "dst.txt")

	// Should not error when source doesn't exist
	if err := copyIfExists(filepath.Join(dir, "nonexistent"), dst); err != nil {
		t.Fatalf("copyIfExists should not error on missing source: %v", err)
	}
}
