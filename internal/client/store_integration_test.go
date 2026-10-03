//go:build integration

package client

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"

	"github.com/jrdn/tt/internal/server"
	"github.com/jrdn/tt/internal/task"
)

// newRemoteStore starts a server on a fresh database with one project and
// returns an HTTP store for it plus the server's directory.
func newRemoteStore(t *testing.T) (*Store, *server.Directory) {
	t.Helper()
	base := os.Getenv("TT_TEST_POSTGRES_URL")
	if base == "" {
		t.Skip("set TT_TEST_POSTGRES_URL to run")
	}
	admin, err := sqlx.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	name := "tt_cli_" + task.NewID()
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
	return NewStore(New(hs.URL, key), "demo"), dir
}

func TestStore_AgainstServer(t *testing.T) {
	s, dir := newRemoteStore(t)
	ctx := context.Background()
	str := func(v string) *string { return &v }

	a, err := s.Create(ctx, "alpha", task.CreateOpts{Priority: 1, PrioritySet: true, DueDate: str("2026-12-01")})
	if err != nil {
		t.Fatal(err)
	}
	if a.Priority != 1 || a.DueDate == nil || *a.DueDate != "2026-12-01" || a.CreatedBy == nil || *a.CreatedBy != "jrdn" {
		t.Errorf("created = %+v", a)
	}
	b, _ := s.Create(ctx, "beta", task.CreateOpts{ParentID: &a.ID, Assignee: str("bot")})
	c, _ := s.Create(ctx, "gamma", task.CreateOpts{})

	if got, err := s.Get(ctx, strings.ToUpper(b.ID[:5])); err != nil || got.ID != b.ID {
		t.Errorf("Get by upper-case prefix = %v, %v", got, err)
	}
	if _, err := s.Get(ctx, "zzzzzzz"); !IsNotFound(err) || !strings.Contains(err.Error(), "no task matching") {
		t.Errorf("Get missing = %v", err)
	}
	if _, err := s.Create(ctx, "bad", task.CreateOpts{Priority: 9, PrioritySet: true}); err == nil {
		t.Error("expected invalid priority to fail")
	}

	if subs, _ := s.List(ctx, task.ListOpts{ParentID: a.ID[:4]}); len(subs) != 1 || subs[0].ID != b.ID {
		t.Errorf("List by parent = %v", subs)
	}

	// Blocked tasks aren't ready; Next skips them until the blocker is done.
	if err := s.AddRelation(ctx, c.ID, "blocks", b.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Next(ctx, "bot"); err != nil || got != nil {
		t.Errorf("Next while blocked = %v, %v", got, err)
	}
	done := task.StatusDone
	if _, err := s.Update(ctx, c.ID, task.UpdateOpts{Status: &done}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Next(ctx, "bot"); err != nil || got == nil || got.ID != b.ID || got.Status != task.StatusInProgress {
		t.Errorf("Next after unblock = %v, %v", got, err)
	}
	if rels, _ := s.GetRelations(ctx, b.ID); len(rels) != 1 {
		t.Errorf("relations = %v", rels)
	}
	if err := s.RemoveRelation(ctx, c.ID, "blocks", b.ID); err != nil {
		t.Fatal(err)
	}

	// Save clears optional fields when they're nil.
	full, _ := s.Get(ctx, b.ID)
	full.Assignee, full.ParentID, full.Title = nil, nil, "beta renamed"
	if err := s.Save(ctx, full); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, b.ID); got.Assignee != nil || got.ParentID != nil || got.Title != "beta renamed" {
		t.Errorf("after Save = %+v", got)
	}
	garbage := task.Status("GARBAGE")
	if _, err := s.Update(ctx, b.ID, task.UpdateOpts{Status: &garbage}); err == nil ||
		!strings.Contains(err.Error(), "invalid status") {
		t.Errorf("invalid status error = %v", err)
	}

	// The server stamps the author from the token.
	if cm, err := s.AddComment(ctx, a.ID, "note", str("mallory")); err != nil || *cm.Author != "jrdn" {
		t.Errorf("comment = %+v, %v", cm, err)
	}
	if cms, _ := s.GetComments(ctx, a.ID); len(cms) != 1 || cms[0].Body != "note" {
		t.Errorf("comments = %v", cms)
	}

	if res, _ := s.Search(ctx, "renamed"); len(res) != 1 || res[0].ID != b.ID {
		t.Errorf("search = %v", res)
	}
	if rec, _ := s.Recent(ctx, 2); len(rec) != 2 {
		t.Errorf("recent = %d tasks, want 2", len(rec))
	}

	if err := s.UpsertExternalRef(ctx, a.ID, "linear", "ENG-1", str("https://linear.app/x/ENG-1")); err != nil {
		t.Fatal(err)
	}
	if got, err := s.FindByExternalRef(ctx, "linear", "ENG-1"); err != nil || got.ID != a.ID {
		t.Errorf("FindByExternalRef = %v, %v", got, err)
	}
	if _, err := s.FindByExternalRef(ctx, "linear", "nope"); err == nil {
		t.Error("expected missing external ref to fail")
	}

	// A revoked JWT is replaced transparently by re-exchanging the key.
	who, _ := dir.PrincipalByHandle(ctx, "jrdn")
	dir.RevokeTokens(ctx, who.ID)
	time.Sleep(5 * time.Millisecond)
	if _, err := s.List(ctx, task.ListOpts{}); err != nil {
		t.Errorf("List after token revocation = %v", err)
	}
}

func TestStore_Subscribe(t *testing.T) {
	s, _ := newRemoteStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := s.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.Create(ctx, "watched", task.CreateOpts{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-events:
		if ev.TaskID != created.ID || ev.Action != "create" {
			t.Errorf("event = %+v, want create of %s", ev, created.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no live event")
	}
}
