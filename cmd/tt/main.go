package main

import (
	"fmt"
	"os"

	"github.com/jrdn/tt/internal/cmd"
	"github.com/jrdn/tt/internal/db"
)

func main() {
	database, err := db.Open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tt: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	if err := db.SetMeta(database, "app_version", version); err != nil {
		fmt.Fprintf(os.Stderr, "tt: set meta: %v\n", err)
		os.Exit(1)
	}

	root := cmd.NewRoot(database)
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
