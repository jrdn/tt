package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jrdn/tt/internal/db"
	"github.com/jrdn/tt/internal/task"
)

// TestActivityFeedListsTicketsAndComments checks the feed endpoint reads the
// local store: newest first, comments with their body and author, creations
// with the ticket's creator.
func TestActivityFeedListsTicketsAndComments(t *testing.T) {
	d, err := db.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	store := task.NewSQLStore(d)
	ctx := context.Background()

	creator := "jrdn"
	first, err := store.Create(ctx, "first ticket", task.CreateOpts{CreatedBy: &creator})
	if err != nil {
		t.Fatal(err)
	}
	author := "hermes"
	if _, err := store.AddComment(ctx, first.ID, "picked this up", &author); err != nil {
		t.Fatal(err)
	}
	second, err := store.Create(ctx, "second ticket", task.CreateOpts{CreatedBy: &creator})
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	(&Server{Resolve: func(*http.Request) (task.Store, error) { return store, nil }}).Register(mux, "/api")
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest("GET", "/api/activity?limit=10", nil))
	if res.Code != 200 {
		t.Fatalf("status = %d: %s", res.Code, res.Body)
	}
	var body struct {
		Activity []task.Activity `json:"activity"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// A comment touches its ticket and a local database keeps no change log, so
	// the feed also carries an "updated" entry for the commented ticket -- but
	// only when the comment landed in a later second than the create, since
	// updated_at has one-second resolution. Assert the entries that must be
	// there instead of a total count that depends on the clock.
	if len(body.Activity) < 3 {
		t.Fatalf("got %d entries, want at least 3: %+v", len(body.Activity), body.Activity)
	}
	byKindTask := map[string]task.Activity{}
	for _, a := range body.Activity {
		key := a.Kind + "/" + a.TaskID
		if _, dup := byKindTask[key]; dup {
			t.Errorf("duplicate %s entry for task %s", a.Kind, a.TaskID)
		}
		byKindTask[key] = a
		if a.TaskID != first.ID && a.TaskID != second.ID {
			t.Errorf("entry for a task the test did not create: %+v", a)
		}
		switch a.Kind {
		case task.ActivityCreated, task.ActivityComment, task.ActivityUpdated:
		default:
			t.Errorf("unexpected entry kind %q: %+v", a.Kind, a)
		}
	}
	entry := func(kind, taskID string) (task.Activity, bool) {
		a, ok := byKindTask[kind+"/"+taskID]
		return a, ok
	}
	comment, ok := entry(task.ActivityComment, first.ID)
	if !ok {
		t.Fatalf("no comment entry: %+v", body.Activity)
	}
	if comment.Body != "picked this up" || comment.TaskTitle != "first ticket" {
		t.Errorf("comment entry = %+v", comment)
	}
	if comment.Actor == nil || *comment.Actor != "hermes" {
		t.Errorf("comment author = %v, want hermes", comment.Actor)
	}
	for _, want := range []struct{ id, title string }{
		{first.ID, "first ticket"},
		{second.ID, "second ticket"},
	} {
		created, ok := entry(task.ActivityCreated, want.id)
		if !ok {
			t.Fatalf("no created entry for %q: %+v", want.title, body.Activity)
		}
		if created.TaskTitle != want.title {
			t.Errorf("created entry = %+v, want title %q", created, want.title)
		}
		if created.Actor == nil || *created.Actor != "jrdn" {
			t.Errorf("created actor = %v, want jrdn", created.Actor)
		}
	}
	for i := 1; i < len(body.Activity); i++ {
		if body.Activity[i-1].At < body.Activity[i].At {
			t.Errorf("feed not newest first: %s before %s", body.Activity[i-1].At, body.Activity[i].At)
		}
	}
}

// TestActivityHandlerLimits checks the limit parameter is bounded and applied.
func TestActivityHandlerLimits(t *testing.T) {
	d, err := db.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	store := task.NewSQLStore(d)
	ctx := context.Background()
	for _, title := range []string{"one", "two", "three"} {
		if _, err := store.Create(ctx, title, task.CreateOpts{}); err != nil {
			t.Fatal(err)
		}
	}

	mux := http.NewServeMux()
	(&Server{Resolve: func(*http.Request) (task.Store, error) { return store, nil }}).Register(mux, "/api")
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest("GET", "/api/activity?limit=2", nil))
	var body struct {
		Activity []task.Activity `json:"activity"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Activity) != 2 {
		t.Fatalf("got %d entries, want 2", len(body.Activity))
	}
}
