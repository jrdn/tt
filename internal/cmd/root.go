package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/jmoiron/sqlx"
	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

// db is the raw SQLite handle, still used by sync for checkpoints and meta.
var db *sqlx.DB
var store task.Store
var jsonOutput bool

// noLocalDB annotates commands that don't use the local SQLite database.
const noLocalDB = "tt/no-local-db"

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
