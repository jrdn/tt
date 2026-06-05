package db

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/jmoiron/sqlx"
)

// DefaultConfigDir returns the directory where tt stores its databases
// (~/.config/tt or $XDG_CONFIG_HOME/tt).
func DefaultConfigDir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "tt"), nil
}

// DBInfo holds the name and full path of a discovered tt database.
type DBInfo struct {
	Name string
	Path string
}

// DiscoverDBs scans the default config directory and returns all *.db files
// as DBInfo entries. The Name is the filename without the .db extension.
func DiscoverDBs() ([]DBInfo, error) {
	dir, err := DefaultConfigDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var dbs []DBInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".db") {
			continue
		}
		dbName := strings.TrimSuffix(name, ".db")
		dbs = append(dbs, DBInfo{
			Name: dbName,
			Path: filepath.Join(dir, name),
		})
	}

	return dbs, nil
}

// OpenDBByName opens a specific tt database from the default config dir
// by its logical name (without the .db extension).
func OpenDBByName(name string) (*sqlx.DB, error) {
	configDir, err := DefaultConfigDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(configDir, name+".db")
	return OpenPath(path)
}
