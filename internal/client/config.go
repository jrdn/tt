// Package client lets the tt CLI, TUI and local web UI work against a tt
// server: project discovery, stored credentials, and an HTTP task.Store.
package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jrdn/tt/internal/config"
)

// ProjectFile is the checked-in file that points a repo at a server project.
const ProjectFile = ".tt.json"

// ProjectConfig says which server and project a directory uses.
type ProjectConfig struct {
	Server  string `json:"server"`
	Project string `json:"project"`
	// Path is where the config came from, or "environment".
	Path string `json:"-"`
}

// FindProject returns the server project for the current directory:
// TT_SERVER and TT_PROJECT if both are set, else the nearest .tt.json in
// this directory or a parent. ok is false in local mode.
func FindProject() (cfg *ProjectConfig, ok bool, err error) {
	srv, proj := os.Getenv("TT_SERVER"), os.Getenv("TT_PROJECT")
	if srv != "" && proj != "" {
		return &ProjectConfig{Server: normalizeServer(srv), Project: proj, Path: "environment"}, true, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return nil, false, err
	}
	for {
		path := filepath.Join(dir, ProjectFile)
		data, err := os.ReadFile(path)
		if err == nil {
			var c ProjectConfig
			if err := json.Unmarshal(data, &c); err != nil {
				return nil, false, fmt.Errorf("%s: %w", path, err)
			}
			if c.Server == "" || c.Project == "" {
				return nil, false, fmt.Errorf("%s: server and project are required", path)
			}
			c.Server = normalizeServer(c.Server)
			if srv != "" {
				c.Server = normalizeServer(srv)
			}
			if proj != "" {
				c.Project = proj
			}
			c.Path = path
			return &c, true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, false, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, false, nil
		}
		dir = parent
	}
}

// WriteProject writes .tt.json into dir.
func WriteProject(dir, server, project string) (string, error) {
	path := filepath.Join(dir, ProjectFile)
	data, _ := json.MarshalIndent(ProjectConfig{Server: normalizeServer(server), Project: project}, "", "  ")
	return path, os.WriteFile(path, append(data, '\n'), 0644)
}

func normalizeServer(s string) string {
	return strings.TrimSuffix(strings.TrimSpace(s), "/")
}

// Credential is a stored login for one server.
type Credential struct {
	Key    string `json:"key"`
	Handle string `json:"handle,omitempty"`
}

func credentialsPath() (string, error) {
	dir, err := config.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "credentials.json"), nil
}

func loadCredentials() (map[string]Credential, error) {
	path, err := credentialsPath()
	if err != nil {
		return nil, err
	}
	creds := map[string]Credential{}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return creds, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return creds, nil
}

// SaveCredential stores (or, with an empty key, removes) the key for a
// server. The file is readable only by the user.
func SaveCredential(server string, c Credential) error {
	creds, err := loadCredentials()
	if err != nil {
		return err
	}
	server = normalizeServer(server)
	if c.Key == "" {
		delete(creds, server)
	} else {
		creds[server] = c
	}
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(creds, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// KeyFor returns the API key to use for server: TT_API_KEY if set, else the
// stored login.
func KeyFor(server string) (string, error) {
	if k := os.Getenv("TT_API_KEY"); k != "" {
		return k, nil
	}
	creds, err := loadCredentials()
	if err != nil {
		return "", err
	}
	if c, ok := creds[normalizeServer(server)]; ok {
		return c.Key, nil
	}
	return "", fmt.Errorf("not logged in to %s: run tt login", server)
}
