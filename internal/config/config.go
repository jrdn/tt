package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ConfigDir returns the tt config directory (~/.config/tt).
func ConfigDir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "tt"), nil
}

// ConfigPath returns the path to the tt config file.
func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// SyncBackendType defines the type of sync backend.
type SyncBackendType string

const (
	BackendDirectory SyncBackendType = "directory"
	BackendSCP       SyncBackendType = "scp"
	BackendS3        SyncBackendType = "s3"
	BackendGitLFS    SyncBackendType = "git_lfs"
)

// SyncConfig holds the configuration for a single sync backend.
type SyncConfig struct {
	Type SyncBackendType `json:"type"`

	// directory backend
	Path string `json:"path,omitempty"`

	// scp backend
	Host       string `json:"host,omitempty"`
	User       string `json:"user,omitempty"`
	Port       int    `json:"port,omitempty"`
	RemotePath string `json:"remote_path,omitempty"`

	// s3 backend
	Bucket    string `json:"bucket,omitempty"`
	Region    string `json:"region,omitempty"`
	AccessKey string `json:"access_key,omitempty"`
	SecretKey string `json:"secret_key,omitempty"`
	Endpoint  string `json:"endpoint,omitempty"` // optional, for S3-compatible services

	// git_lfs backend
	Branch  string `json:"branch,omitempty"`   // git branch for sync
	Remote  string `json:"remote,omitempty"`   // git remote name (default: origin); used when RepoURL is empty
	RepoURL string `json:"repo_url,omitempty"` // explicit remote URL; enables CWD-independent sync
}

// DBConfig holds the configuration for a single database.
type DBConfig struct {
	Sync *SyncConfig `json:"sync,omitempty"`
}

// TTConfig is the top-level configuration structure.
type TTConfig struct {
	Databases map[string]*DBConfig `json:"databases,omitempty"`
	// DefaultServer is the tt server account commands use when neither
	// --server, TT_SERVER nor a repo's .tt.json names one. Set by tt login.
	DefaultServer string `json:"default_server,omitempty"`
}

// Load reads the config file from disk. Returns an empty config if the file
// doesn't exist.
func Load() (*TTConfig, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}

	cfg := &TTConfig{
		Databases: make(map[string]*DBConfig),
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.Databases == nil {
		cfg.Databases = make(map[string]*DBConfig)
	}

	return cfg, nil
}

// Save writes the config to disk.
func (c *TTConfig) Save() error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

// GetSyncConfig returns the sync configuration for a given database name.
func (c *TTConfig) GetSyncConfig(dbName string) (*SyncConfig, error) {
	dbCfg, ok := c.Databases[dbName]
	if !ok {
		return nil, fmt.Errorf("no sync config for database %q", dbName)
	}
	if dbCfg.Sync == nil {
		return nil, fmt.Errorf("no sync backend configured for database %q", dbName)
	}
	return dbCfg.Sync, nil
}

// SetSyncConfig sets the sync configuration for a given database name.
func (c *TTConfig) SetSyncConfig(dbName string, sync *SyncConfig) {
	if c.Databases == nil {
		c.Databases = make(map[string]*DBConfig)
	}
	if c.Databases[dbName] == nil {
		c.Databases[dbName] = &DBConfig{}
	}
	c.Databases[dbName].Sync = sync
}

// UnsetSyncConfig removes the sync configuration for a given database name.
func (c *TTConfig) UnsetSyncConfig(dbName string) {
	if dbCfg, ok := c.Databases[dbName]; ok {
		dbCfg.Sync = nil
	}
}

// Validate checks that the sync config has the required fields for its type.
func (s *SyncConfig) Validate() error {
	switch s.Type {
	case BackendDirectory:
		if s.Path == "" {
			return fmt.Errorf("directory backend requires --path")
		}
	case BackendSCP:
		if s.Host == "" {
			return fmt.Errorf("scp backend requires --host")
		}
		if s.RemotePath == "" {
			return fmt.Errorf("scp backend requires --remote-path")
		}
	case BackendS3:
		if s.Bucket == "" {
			return fmt.Errorf("s3 backend requires --bucket")
		}
		if s.Region == "" {
			return fmt.Errorf("s3 backend requires --region")
		}
	case BackendGitLFS:
		if s.Branch == "" {
			return fmt.Errorf("git_lfs backend requires --branch")
		}
	default:
		return fmt.Errorf("unknown sync backend type: %q", s.Type)
	}
	return nil
}
