package cmd

import (
	"context"
	"fmt"
	"sort"

	"github.com/fatih/color"
	"github.com/jrdn/tt/internal/client"
	ttDB "github.com/jrdn/tt/internal/db"
	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

// overviewOpts carries the filters shared by the local and server paths.
type overviewOpts struct {
	status   string
	parentID string
	assignee string
	all      bool
	ready    bool
}

func (o overviewOpts) listOpts() task.ListOpts {
	return task.ListOpts{
		Status:   o.status,
		ParentID: o.parentID,
		Assignee: o.assignee,
		All:      o.all,
		Ready:    o.ready,
	}
}

// overviewRow is one task plus the scope it came from: a local database name,
// or a project slug when reading from a server.
type overviewRow struct {
	Scope string
	Task  task.Task
}

// qualifiedID is the <scope>.<task id> label the overview prints. Short task
// ids are only unique within a scope, so the prefix is the point of the
// command.
func (r overviewRow) qualifiedID() string { return r.Scope + "." + r.Task.ID }

// sortOverviewRows orders rows by scope, then status, then creation time, so
// the output does not depend on the order projects came back in.
func sortOverviewRows(rows []overviewRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch {
		case a.Scope != b.Scope:
			return a.Scope < b.Scope
		case a.Task.Status != b.Task.Status:
			return a.Task.Status < b.Task.Status
		}
		return a.Task.CreatedAt < b.Task.CreatedAt
	})
}

// taskLister is the part of a project store the overview needs. Both the
// SQLite store and the server client store satisfy it.
type taskLister interface {
	List(ctx context.Context, opts task.ListOpts) ([]task.Task, error)
}

// projectLister lists the projects the key can see (server mode only).
type projectLister interface {
	Projects(ctx context.Context) ([]client.Project, error)
}

// overviewAcrossProjects collects tasks from every visible project. A project
// that fails to list is reported and skipped, so one broken project does not
// hide the rest of the board.
func overviewAcrossProjects(ctx context.Context, c projectLister, storeFor func(slug string) taskLister,
	opts overviewOpts, only string) ([]overviewRow, []error, error) {
	projects, err := c.Projects(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list projects: %w", err)
	}

	slugs := make([]string, 0, len(projects))
	for _, p := range projects {
		if only != "" && p.Slug != only {
			continue
		}
		slugs = append(slugs, p.Slug)
	}
	if only != "" && len(slugs) == 0 {
		return nil, nil, fmt.Errorf("no project named %q visible to this key", only)
	}
	sort.Strings(slugs)

	var rows []overviewRow
	var errs []error
	for _, slug := range slugs {
		tasks, err := storeFor(slug).List(ctx, opts.listOpts())
		if err != nil {
			errs = append(errs, fmt.Errorf("list %s: %w", slug, err))
			continue
		}
		for _, t := range tasks {
			rows = append(rows, overviewRow{Scope: slug, Task: t})
		}
	}
	sortOverviewRows(rows)
	return rows, errs, nil
}

// overviewJSONEntry keeps the pre-server shape ("database") and adds the
// project slug plus the qualified id for scripts.
type overviewJSONEntry struct {
	Database string     `json:"database,omitempty"`
	Project  string     `json:"project,omitempty"`
	ID       string     `json:"id"`
	Task     *task.Task `json:"task"`
}

func overviewJSON(rows []overviewRow, serverMode bool) []overviewJSONEntry {
	out := make([]overviewJSONEntry, 0, len(rows))
	for i := range rows {
		e := overviewJSONEntry{ID: rows[i].qualifiedID(), Task: &rows[i].Task}
		if serverMode {
			e.Project = rows[i].Scope
		} else {
			e.Database = rows[i].Scope
		}
		out = append(out, e)
	}
	return out
}

func printOverviewTable(rows []overviewRow) {
	header := color.New(color.Faint)
	header.Printf("%-26s  %s  %-13s  %s\n", "ID", "P", "STATUS", "TITLE")
	for _, r := range rows {
		t := r.Task
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

		fmt.Printf("%-26s  %s  %s  %s%s\n", r.qualifiedID(), priLabel, statusStr, t.Title, assignee)
	}
}

func printOverviewWarnings(errs []error) {
	for _, e := range errs {
		fmt.Fprintf(color.Output, "%s\n", color.New(color.FgYellow).Sprint("warn: "+e.Error()))
	}
}

func newGlobalOverviewCmd() *cobra.Command {
	opts := overviewOpts{}
	var (
		dbFilter      string
		projectFilter string
	)

	cmd := &cobra.Command{
		Use:     "global-overview",
		Aliases: []string{"go", "overview"},
		Short:   "List tasks across all tt databases, or all server projects",
		Long: `Aggregates tasks from every tt database in the default config directory, or
— when this directory uses a tt server — from every project the key can see.

Tasks are displayed with a qualified ID: <db_name>.<task_id> for local
databases and <project>.<task_id> on a server, ordered by scope, then status,
then creation time.

Use --db to filter to one local database, or --project to filter to one project
on a server (--db is accepted as the project filter there too).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			serverMode := remote != nil
			var (
				rows []overviewRow
				errs []error
			)

			if serverMode {
				only := projectFilter
				if only == "" {
					only = dbFilter
				}
				key, err := client.KeyFor(remote.Server)
				if err != nil {
					return err
				}
				c := client.New(remote.Server, key)
				rows, errs, err = overviewAcrossProjects(cmd.Context(), c,
					func(slug string) taskLister { return client.NewStore(c, slug) }, opts, only)
				if err != nil {
					return err
				}
			} else {
				if projectFilter != "" {
					return fmt.Errorf("--project names a server project; this directory uses a local database (use --db)")
				}
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

				for _, d := range dbs {
					database, err := ttDB.OpenDBByName(d.Name)
					if err != nil {
						errs = append(errs, fmt.Errorf("open %s: %w", d.Name, err))
						continue
					}
					tasks, err := task.NewSQLStore(database).List(cmd.Context(), opts.listOpts())
					database.Close()
					if err != nil {
						errs = append(errs, fmt.Errorf("list %s: %w", d.Name, err))
						continue
					}
					for _, t := range tasks {
						rows = append(rows, overviewRow{Scope: d.Name, Task: t})
					}
				}
			}

			if jsonOutput {
				return printJSON(overviewJSON(rows, serverMode))
			}

			if len(rows) == 0 {
				fmt.Println("no tasks")
				return nil
			}

			// Non-fatal errors (an unreadable database or project) are warnings.
			printOverviewWarnings(errs)
			printOverviewTable(rows)
			return nil
		},
	}

	cmd.Flags().StringVar(&opts.status, "status", "", "Filter by status (backlog, open, ready, in_progress, in_review, done, cancelled)")
	cmd.Flags().StringVar(&opts.parentID, "parent", "", "Show subtasks of this task")
	cmd.Flags().StringVarP(&opts.assignee, "assignee", "a", "", "Filter by assignee handle")
	cmd.Flags().BoolVar(&opts.all, "all", false, "Include all statuses")
	cmd.Flags().BoolVarP(&opts.ready, "ready", "r", false, "Open tasks with no unresolved blockers")
	cmd.Flags().StringVar(&dbFilter, "db", "", "Filter to a specific database by name (a project slug on a server)")
	cmd.Flags().StringVar(&projectFilter, "project", "", "Filter to a specific server project slug")

	return cmd
}
