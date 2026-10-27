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
	if _, err := store.Create(ctx, "second ticket", task.CreateOpts{CreatedBy: &creator}); err != nil {
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
	if len(body.Activity) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(body.Activity), body.Activity)
	}
	byKind := map[string]task.Activity{}
	for _, a := range body.Activity {
		byKind[a.Kind] = a
	}
	comment, ok := byKind[task.ActivityComment]
	if !ok {
		t.Fatalf("no comment entry: %+v", body.Activity)
	}
	if comment.Body != "picked this up" || comment.TaskTitle != "first ticket" {
		t.Errorf("comment entry = %+v", comment)
	}
	if comment.Actor == nil || *comment.Actor != "hermes" {
		t.Errorf("comment author = %v, want hermes", comment.Actor)
	}
	created, ok := byKind[task.ActivityCreated]
	if !ok {
		t.Fatalf("no created entry: %+v", body.Activity)
	}
	if created.Actor == nil || *created.Actor != "jrdn" {
		t.Errorf("created actor = %v, want jrdn", created.Actor)
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
