package config

import (
	"os"
	"path/filepath"
	"testing"
)

func tempConfig(t *testing.T) {
	t.Helper()
	// Override the config dir to a temp directory for the duration of the test.
	// We do this by temporarily replacing os.UserConfigDir via an env-var trick.
	// Since config.go calls os.UserConfigDir directly, we patch ConfigPath
	// by using a custom test helper. For this test, we'll use a local approach:
	// create the file in the real config dir with a unique name, test, then remove.
	// Instead, let's just create a temp dir and override via env var.
	//
	// Since our code doesn't support env override, we create the config file
	// in the real config dir and clean up after.
	cfgDir, err := ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir: %v", err)
	}
	cfgPath := filepath.Join(cfgDir, "config.json")

	// Save existing config if it exists
	var existing []byte
	if data, err := os.ReadFile(cfgPath); err == nil {
		existing = data
	} else if !os.IsNotExist(err) {
		t.Fatalf("read existing config: %v", err)
	}

	t.Cleanup(func() {
		if existing != nil {
			os.WriteFile(cfgPath, existing, 0600)
		} else {
			os.Remove(cfgPath)
		}
	})
}

func TestLoad_NoConfigFile(t *testing.T) {
	tempConfig(t)

	// Remove config file
	cfgPath, _ := ConfigPath()
	os.Remove(cfgPath)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Databases == nil {
		t.Error("expected non-nil Databases map")
	}
}

func TestSaveAndLoad(t *testing.T) {
	tempConfig(t)

	cfg := &TTConfig{
		Databases: make(map[string]*DBConfig),
	}
	cfg.SetSyncConfig("mydb", &SyncConfig{
		Type: BackendDirectory,
		Path: "/tmp/sync",
	})

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	sync, err := loaded.GetSyncConfig("mydb")
	if err != nil {
		t.Fatalf("GetSyncConfig: %v", err)
	}
	if sync.Type != BackendDirectory {
		t.Errorf("type = %q, want %q", sync.Type, BackendDirectory)
	}
	if sync.Path != "/tmp/sync" {
		t.Errorf("path = %q, want %q", sync.Path, "/tmp/sync")
	}
}

func TestSetAndGetSyncConfig(t *testing.T) {
	tempConfig(t)

	cfg := &TTConfig{Databases: make(map[string]*DBConfig)}

	cfg.SetSyncConfig("db1", &SyncConfig{Type: BackendSCP, Host: "example.com", RemotePath: "/data"})
	cfg.SetSyncConfig("db2", &SyncConfig{Type: BackendS3, Bucket: "mybucket", Region: "us-east-1"})

	s1, err := cfg.GetSyncConfig("db1")
	if err != nil {
		t.Fatalf("GetSyncConfig db1: %v", err)
	}
	if s1.Host != "example.com" {
		t.Errorf("host = %q, want example.com", s1.Host)
	}

	s2, err := cfg.GetSyncConfig("db2")
	if err != nil {
		t.Fatalf("GetSyncConfig db2: %v", err)
	}
	if s2.Bucket != "mybucket" {
		t.Errorf("bucket = %q, want mybucket", s2.Bucket)
	}
}

func TestGetSyncConfig_NotConfigured(t *testing.T) {
	cfg := &TTConfig{Databases: make(map[string]*DBConfig)}

	_, err := cfg.GetSyncConfig("nonexistent")
	if err == nil {
		t.Error("expected error for unconfigured database")
	}
}

func TestGetSyncConfig_NoSync(t *testing.T) {
	cfg := &TTConfig{
		Databases: map[string]*DBConfig{
			"db1": {},
		},
	}

	_, err := cfg.GetSyncConfig("db1")
	if err == nil {
		t.Error("expected error for database without sync config")
	}
}

func TestUnsetSyncConfig(t *testing.T) {
	cfg := &TTConfig{Databases: make(map[string]*DBConfig)}
	cfg.SetSyncConfig("db1", &SyncConfig{Type: BackendDirectory, Path: "/tmp"})

	cfg.UnsetSyncConfig("db1")

	_, err := cfg.GetSyncConfig("db1")
	if err == nil {
		t.Error("expected error after unset")
	}
}

func TestUnsetSyncConfig_NoPanic(t *testing.T) {
	cfg := &TTConfig{Databases: make(map[string]*DBConfig)}

	// Should not panic
	cfg.UnsetSyncConfig("nonexistent")
}

func TestValidate_Directory(t *testing.T) {
	tests := []struct {
		name    string
		cfg     SyncConfig
		wantErr bool
	}{
		{"valid", SyncConfig{Type: BackendDirectory, Path: "/tmp"}, false},
		{"missing path", SyncConfig{Type: BackendDirectory}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidate_SCP(t *testing.T) {
	tests := []struct {
		name    string
		cfg     SyncConfig
		wantErr bool
	}{
		{"valid", SyncConfig{Type: BackendSCP, Host: "h", RemotePath: "/p"}, false},
		{"missing host", SyncConfig{Type: BackendSCP, RemotePath: "/p"}, true},
		{"missing remote path", SyncConfig{Type: BackendSCP, Host: "h"}, true},
		{"missing both", SyncConfig{Type: BackendSCP}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidate_S3(t *testing.T) {
	tests := []struct {
		name    string
		cfg     SyncConfig
		wantErr bool
	}{
		{"valid", SyncConfig{Type: BackendS3, Bucket: "b", Region: "r"}, false},
		{"missing bucket", SyncConfig{Type: BackendS3, Region: "r"}, true},
		{"missing region", SyncConfig{Type: BackendS3, Bucket: "b"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidate_GitLFS(t *testing.T) {
	tests := []struct {
		name    string
		cfg     SyncConfig
		wantErr bool
	}{
		{"valid", SyncConfig{Type: BackendGitLFS, Branch: "sync"}, false},
		{"missing branch", SyncConfig{Type: BackendGitLFS}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidate_UnknownType(t *testing.T) {
	cfg := SyncConfig{Type: "unknown"}
	err := cfg.Validate()
	if err == nil {
		t.Error("expected error for unknown type")
	}
}

func TestRoundTripJSON(t *testing.T) {
	tempConfig(t)

	cfg := &TTConfig{Databases: make(map[string]*DBConfig)}
	cfg.SetSyncConfig("db1", &SyncConfig{
		Type:       BackendSCP,
		Host:       "server.com",
		User:       "deploy",
		Port:       2222,
		RemotePath: "/home/deploy/tt.db",
	})
	cfg.SetSyncConfig("db2", &SyncConfig{
		Type:      BackendS3,
		Bucket:    "my-bucket",
		Region:    "eu-west-1",
		Endpoint:  "https://s3.eu-west-1.amazonaws.com",
		AccessKey: "AKIAIOSFODNN7EXAMPLE",
		SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	})
	cfg.SetSyncConfig("db3", &SyncConfig{
		Type:   BackendGitLFS,
		Branch: "tt-sync-data",
	})

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Verify SCP config
	scp, _ := loaded.GetSyncConfig("db1")
	if scp.Host != "server.com" || scp.User != "deploy" || scp.Port != 2222 || scp.RemotePath != "/home/deploy/tt.db" {
		t.Errorf("SCP config mismatch: %+v", scp)
	}

	// Verify S3 config
	s3, _ := loaded.GetSyncConfig("db2")
	if s3.Bucket != "my-bucket" || s3.Region != "eu-west-1" || s3.Endpoint != "https://s3.eu-west-1.amazonaws.com" {
		t.Errorf("S3 config mismatch: %+v", s3)
	}

	// Verify GitLFS config
	git, _ := loaded.GetSyncConfig("db3")
	if git.Type != BackendGitLFS || git.Branch != "tt-sync-data" {
		t.Errorf("GitLFS config mismatch: %+v", git)
	}
}
