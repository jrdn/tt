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
	"time"

	"github.com/zalando/go-keyring"

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

// DefaultServer returns the server set by the last tt login, if any.
func DefaultServer() (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	return cfg.DefaultServer, nil
}

// SetDefaultServer records (or, with "", clears) the default server.
func SetDefaultServer(server string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.DefaultServer = normalizeServer(server)
	return cfg.Save()
}

// Credential is a stored login for one server. With Storage "keychain" the
// key lives in the OS keychain and Key is empty on disk.
type Credential struct {
	Key     string `json:"key,omitempty"`
	Handle  string `json:"handle,omitempty"`
	Storage string `json:"storage,omitempty"`
}

const (
	keyringService  = "tt"
	storageKeychain = "keychain"
)

// Keychain calls go through these so tests can simulate a stuck keychain.
var (
	keyringSet    = keyring.Set
	keyringGet    = keyring.Get
	keyringDelete = keyring.Delete
)

// keyringTimeout bounds keychain calls: a locked keychain can wait on a
// dialog nobody sees (e.g. over SSH), which would otherwise hang tt.
var keyringTimeout = 5 * time.Second

// withTimeout runs a keychain call, giving up after keyringTimeout.
func withTimeout[T any](f func() (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := f()
		done <- result{v, err}
	}()
	select {
	case r := <-done:
		return r.v, r.err
	case <-time.After(keyringTimeout):
		var zero T
		return zero, fmt.Errorf("the OS keychain did not respond within %s (is it locked?)", keyringTimeout)
	}
}

func kcSet(server, key string) error {
	_, err := withTimeout(func() (struct{}, error) { return struct{}{}, keyringSet(keyringService, server, key) })
	return err
}

func kcGet(server string) (string, error) {
	return withTimeout(func() (string, error) { return keyringGet(keyringService, server) })
}

func kcDelete(server string) error {
	_, err := withTimeout(func() (struct{}, error) { return struct{}{}, keyringDelete(keyringService, server) })
	return err
}

// useKeyring is false when TT_KEYRING=off, which forces the file (e.g. on
// shared CI machines).
func useKeyring() bool {
	return os.Getenv("TT_KEYRING") != "off"
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

func writeCredentials(creds map[string]Credential) error {
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

// SaveCredential stores the key for a server in the OS keychain, falling
// back to credentials.json (mode 0600) when no keychain is available. An
// empty key removes the login. It returns where the key was stored.
func SaveCredential(server string, c Credential) (string, error) {
	creds, err := loadCredentials()
	if err != nil {
		return "", err
	}
	server = normalizeServer(server)
	path, err := credentialsPath()
	if err != nil {
		return "", err
	}
	if c.Key == "" {
		if useKeyring() || creds[server].Storage == storageKeychain {
			if err := kcDelete(server); err != nil && !errors.Is(err, keyring.ErrNotFound) {
				fmt.Fprintf(os.Stderr, "tt: could not remove key from the OS keychain: %v\n", err)
			}
		}
		delete(creds, server)
		return "", writeCredentials(creds)
	}
	where := path
	if useKeyring() {
		if err := kcSet(server, c.Key); err == nil {
			c = Credential{Handle: c.Handle, Storage: storageKeychain}
			where = "the OS keychain"
		} else {
			fmt.Fprintf(os.Stderr, "tt: OS keychain unavailable (%v); storing the key in %s\n", err, path)
		}
	}
	if c.Storage != storageKeychain {
		c.Storage = ""
	}
	creds[server] = c
	return where, writeCredentials(creds)
}

// KeyFor returns the API key to use for server: TT_API_KEY if set, else the
// stored login. Keys still stored in plain text are moved to the keychain.
func KeyFor(server string) (string, error) {
	if k := os.Getenv("TT_API_KEY"); k != "" {
		return k, nil
	}
	creds, err := loadCredentials()
	if err != nil {
		return "", err
	}
	server = normalizeServer(server)
	c, ok := creds[server]
	if !ok {
		return "", fmt.Errorf("not logged in to %s: run tt login", server)
	}
	if c.Storage == storageKeychain {
		key, err := kcGet(server)
		if err != nil {
			return "", fmt.Errorf("read key for %s from the OS keychain: %w (unlock it, or run tt login; TT_KEYRING=off uses a file instead)", server, err)
		}
		return key, nil
	}
	if useKeyring() {
		// Best effort: leaves the file untouched if the keychain refuses.
		if err := kcSet(server, c.Key); err == nil {
			creds[server] = Credential{Handle: c.Handle, Storage: storageKeychain}
			writeCredentials(creds)
		}
	}
	return c.Key, nil
}
