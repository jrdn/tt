//go:build integration

package tui_test

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"

	"github.com/jrdn/tt/internal/client"
	"github.com/jrdn/tt/internal/server"
	"github.com/jrdn/tt/internal/task"
)

// TestTUI_ServerStore runs the same TUI session as the local test, through
// the HTTP store against a real server (set TT_TEST_POSTGRES_URL).
func TestTUI_ServerStore(t *testing.T) {
	base := os.Getenv("TT_TEST_POSTGRES_URL")
	if base == "" {
		t.Skip("set TT_TEST_POSTGRES_URL to run")
	}
	admin, err := sqlx.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	name := "tt_tui_" + task.NewID()
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	srv, err := server.New(ctx, server.Config{
		DatabaseURL: strings.Replace(base, "/tt_test", "/"+name, 1),
		MasterKey:   []byte("0123456789abcdef0123456789abcdef"),
	})
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		hs.CloseClientConnections() // the live-update stream never ends on its own
		hs.Close()
		srv.Close()
		admin.Exec("DROP DATABASE " + name + " WITH (FORCE)")
		admin.Close()
	})
	dir := srv.Directory()
	u, _ := dir.CreateUser(ctx, "jrdn", nil, true)
	if _, err := dir.CreateProject(ctx, "demo", "demo", u); err != nil {
		t.Fatal(err)
	}
	_, key, _ := dir.CreateKey(ctx, u, u, "", nil, "", time.Time{})
	runTUI(t, client.NewStore(client.New(hs.URL, key), "demo"))
}
