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

func NewRoot(database *sqlx.DB) *cobra.Command {
	db = database
	store = task.NewSQLStore(database)

	root := &cobra.Command{
		Use:   "tt",
		Short: "Local-first task manager",
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
	)

	return root
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("json encode: %w", err)
	}
	return nil
}
