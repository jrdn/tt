package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/jmoiron/sqlx"
	"github.com/jrdn/tt/internal/client"
	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

// db is the raw SQLite handle, still used by sync for checkpoints and meta.
var db *sqlx.DB
var store task.Store
var jsonOutput bool

// noLocalDB annotates commands that manage their own storage (or none), so
// no store is opened for them.
const noLocalDB = "tt/no-local-db"

// localOnly annotates commands that only make sense for a local database.
const localOnly = "tt/local-only"

// remote is set when this directory uses a tt server.
var remote *client.ProjectConfig

// NewRoot builds the command tree. openDB is called before any command
// that uses the local database.
func NewRoot(openDB func() (*sqlx.DB, error)) *cobra.Command {
	root := &cobra.Command{
		Use:   "tt",
		Short: "Local-first task manager",
		PersistentPreRunE: func(c *cobra.Command, args []string) error {
			for p := c; p != nil; p = p.Parent() {
				if p.Annotations[noLocalDB] != "" {
					return nil
				}
			}
			cfg, ok, err := client.FindProject()
			if err != nil {
				c.SilenceUsage = true
				return err
			}
			if ok {
				if c.Annotations[localOnly] != "" || (c.Parent() != nil && c.Parent().Annotations[localOnly] != "") {
					c.SilenceUsage = true
					return fmt.Errorf("%s works on local databases; this directory uses the tt server %s (from %s)",
						c.CommandPath(), cfg.Server, cfg.Path)
				}
				key, err := client.KeyFor(cfg.Server)
				if err != nil {
					c.SilenceUsage = true
					return err
				}
				remote = cfg
				store = client.NewStore(client.New(cfg.Server, key), cfg.Project)
				return nil
			}
			database, err := openDB()
			if err != nil {
				c.SilenceUsage = true
				return err
			}
			db = database
			store = task.NewSQLStore(database)
			return nil
		},
	}

	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output as JSON")

	root.AddCommand(
		newAddCmd(),
		newLsCmd(),
		newGlobalOverviewCmd(),
		newShowCmd(),
		newDoneCmd(),
		newUpdateCmd(),
		newCommentCmd(),
		newRelateCmd(),
		newLinkCmd(),
		newWorktreeCmd(),
		newInitCmd(),
		newEditCmd(),
		newSearchCmd(),
		newAssignCmd(),
		newClaimCmd(),
		newNextCmd(),
		newPrimeCmd(),
		newCancelCmd(),
		newReopenCmd(),
		newUnrelateCmd(),
		newGraphCmd(),
		newWebCmd(),
		newSyncCmd(),
		newImportCmd(),
		newTuiCmd(),
		newServerCmd(),
		newKeyCmd(),
		newLoginCmd(),
		newLogoutCmd(),
		newWhoamiCmd(),
		newAgentCmd(),
		newProjectCmd(),
		newExportCmd(),
	)

	return root
}

// CloseDB closes the local database if a command opened it.
func CloseDB() {
	if db != nil {
		db.Close()
	}
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("json encode: %w", err)
	}
	return nil
}
