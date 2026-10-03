package main

import (
	"fmt"
	"os"

	"github.com/jmoiron/sqlx"

	"github.com/jrdn/tt/internal/cmd"
	"github.com/jrdn/tt/internal/db"
)

func main() {
	root := cmd.NewRoot(openLocalDB)
	err := root.Execute()
	cmd.CloseDB()
	if err != nil {
		os.Exit(1)
	}
}

func openLocalDB() (*sqlx.DB, error) {
	database, err := db.Open()
	if err != nil {
		return nil, err
	}
	if err := db.SetMeta(database, "app_version", version); err != nil {
		database.Close()
		return nil, fmt.Errorf("set meta: %w", err)
	}
	return database, nil
}
