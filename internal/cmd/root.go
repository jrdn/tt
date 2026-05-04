package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/jmoiron/sqlx"
	"github.com/spf13/cobra"
)

var db *sqlx.DB
var jsonOutput bool

func NewRoot(database *sqlx.DB) *cobra.Command {
	db = database

	root := &cobra.Command{
		Use:   "tt",
		Short: "Local-first task manager",
	}

	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output as JSON")

	root.AddCommand(
		newAddCmd(),
		newLsCmd(),
		newShowCmd(),
		newDoneCmd(),
		newUpdateCmd(),
		newCommentCmd(),
		newRelateCmd(),
		newInitCmd(),
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
