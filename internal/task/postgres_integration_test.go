//go:build integration

package task

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/jrdn/tt/internal/db"
)

// openTestPostgres gives each test its own project schema so tests stay
// isolated while sharing one Postgres instance.
func openTestPostgres(t *testing.T, url string) *sqlx.DB {
	t.Helper()
	schema := "t_" + NewID() + NewID()
	d, err := db.OpenProjectSchema(context.Background(), url, schema)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() {
		d.Exec("DROP SCHEMA " + schema + " CASCADE")
		d.Close()
	})
	return d
}

// TestNext_ConcurrentClaimsArePostgresSafe checks that concurrent Next calls
// never hand the same task to two callers — the property agents rely on when
// several of them pull from one server.
func TestNext_ConcurrentClaimsArePostgresSafe(t *testing.T) {
	if os.Getenv("TT_TEST_POSTGRES_URL") == "" {
		t.Skip("set TT_TEST_POSTGRES_URL to run")
	}
	d := openTestDB(t)
	handle := "bot"
	const tasks, workers = 30, 8
	for i := 0; i < tasks; i++ {
		if _, err := Create(d, fmt.Sprintf("task %d", i), CreateOpts{Assignee: &handle}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	var mu sync.Mutex
	claimed := map[string]int{}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				got, err := Next(d, handle)
				if err != nil {
					t.Errorf("Next: %v", err)
					return
				}
				if got == nil {
					return
				}
				mu.Lock()
				claimed[got.ID]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(claimed) != tasks {
		t.Errorf("claimed %d distinct tasks, want %d", len(claimed), tasks)
	}
	for id, n := range claimed {
		if n != 1 {
			t.Errorf("task %s claimed %d times", id, n)
		}
	}
}

// TestTimestamps_RoundTrip checks that timestamps read back exactly as
// written. Postgres stores them as timestamptz, so this guards the --json
// output format against drifting from SQLite's RFC3339 "Z" strings.
func TestTimestamps_RoundTrip(t *testing.T) {
	d := openTestDB(t)
	created, err := Create(d, "task", CreateOpts{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	done := StatusDone
	updated, err := Update(d, created.ID, UpdateOpts{Status: &done})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := Get(d, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CreatedAt != created.CreatedAt {
		t.Errorf("created_at = %q, want %q", got.CreatedAt, created.CreatedAt)
	}
	if got.UpdatedAt != updated.UpdatedAt {
		t.Errorf("updated_at = %q, want %q", got.UpdatedAt, updated.UpdatedAt)
	}
	if got.ClosedAt == nil || *got.ClosedAt != *updated.ClosedAt {
		t.Errorf("closed_at = %v, want %q", got.ClosedAt, *updated.ClosedAt)
	}
}

// Due dates read back as YYYY-MM-DD on both backends; on Postgres the
// column is a real DATE.
func TestDueDate_RoundTrip(t *testing.T) {
	d := openTestDB(t)
	due := "2026-12-01"
	created, err := Create(d, "task", CreateOpts{DueDate: &due})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Get(d, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DueDate == nil || *got.DueDate != "2026-12-01" {
		t.Errorf("due_date = %v, want 2026-12-01", got.DueDate)
	}
	if d.DriverName() == "pgx" {
		var typ string
		d.Get(&typ, `SELECT data_type FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'tasks' AND column_name = 'due_date'`)
		if typ != "date" {
			t.Errorf("due_date column type = %q, want date", typ)
		}
	}
}
