package cmd

import (
	"fmt"

	"github.com/fatih/color"
	ttDB "github.com/jrdn/tt/internal/db"
	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

func newGlobalOverviewCmd() *cobra.Command {
	var (
		status   string
		parentID string
		assignee string
		all      bool
		ready    bool
		dbFilter string
	)

	cmd := &cobra.Command{
		Use:   "global-overview",
		Aliases: []string{"go", "overview"},
		Short:   "List tasks across all tt databases",
		Long: `Aggregates tasks from every tt database in the default config directory.
Tasks are displayed with a qualified ID: <db_name>.<task_id>.

Use the --db flag to filter to a specific database name.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			dbs, err := ttDB.DiscoverDBs()
			if err != nil {
				return fmt.Errorf("discover databases: %w", err)
			}
			if len(dbs) == 0 {
				fmt.Println("no tt databases found")
				return nil
			}

			// Optional: filter to a specific DB by name
			if dbFilter != "" {
				var filtered []ttDB.DBInfo
				for _, d := range dbs {
					if d.Name == dbFilter {
						filtered = append(filtered, d)
						break
					}
				}
				if len(filtered) == 0 {
					return fmt.Errorf("no database named %q found", dbFilter)
				}
				dbs = filtered
			}

			type annotatedTask struct {
				DBName string
				Task   task.Task
			}

			var allTasks []annotatedTask
			var errs []error

			for _, d := range dbs {
				database, err := ttDB.OpenDBByName(d.Name)
				if err != nil {
					errs = append(errs, fmt.Errorf("open %s: %w", d.Name, err))
					continue
				}

				tasks, err := task.NewSQLStore(database).List(cmd.Context(), task.ListOpts{
					Status:   status,
					ParentID: parentID,
					Assignee: assignee,
					All:      all,
					Ready:    ready,
				})
				database.Close()

				if err != nil {
					errs = append(errs, fmt.Errorf("list %s: %w", d.Name, err))
					continue
				}

				for _, t := range tasks {
					allTasks = append(allTasks, annotatedTask{
						DBName: d.Name,
						Task:   t,
					})
				}
			}

			if jsonOutput {
				type jsonEntry struct {
					DB string       `json:"database"`
					Task *task.Task `json:"task"`
				}
				var out []jsonEntry
				for _, at := range allTasks {
					out = append(out, jsonEntry{DB: at.DBName, Task: &at.Task})
				}
				return printJSON(out)
			}

			if len(allTasks) == 0 {
				fmt.Println("no tasks")
				return nil
			}

			// Print any non-fatal errors as warnings
			for _, e := range errs {
				fmt.Fprintf(color.Output, "%s\n", color.New(color.FgYellow).Sprint("warn: "+e.Error()))
			}

			header := color.New(color.Faint)
			header.Printf("%-26s  %s  %-13s  %s\n", "ID", "P", "STATUS", "TITLE")
			for _, at := range allTasks {
				t := at.Task
				assignee := ""
				if t.Assignee != nil {
					assignee = color.New(color.FgHiBlack).Sprint("  @" + *t.Assignee)
				}

				p := t.Priority
				if p < 0 || p > 3 {
					p = 2
				}
				priLabel := priorityColors[p].Sprint(priorityLabels[p])

				sc, ok := statusColors[t.Status]
				if !ok {
					sc = color.New(color.Reset)
				}
				statusStr := sc.Sprintf("%-13s", string(t.Status))

				qualifiedID := at.DBName + "." + t.ID
				fmt.Printf("%-26s  %s  %s  %s%s\n",
					qualifiedID,
					priLabel,
					statusStr,
					t.Title,
					assignee,
				)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&status, "status", "", "Filter by status (backlog, open, ready, in_progress, in_review, done, cancelled)")
	cmd.Flags().StringVar(&parentID, "parent", "", "Show subtasks of this task")
	cmd.Flags().StringVarP(&assignee, "assignee", "a", "", "Filter by assignee handle")
	cmd.Flags().BoolVar(&all, "all", false, "Include all statuses")
	cmd.Flags().BoolVarP(&ready, "ready", "r", false, "Open tasks with no unresolved blockers")
	cmd.Flags().StringVar(&dbFilter, "db", "", "Filter to a specific database by name")

	return cmd
}
