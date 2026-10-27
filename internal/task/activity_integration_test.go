//go:build integration

package task

import (
	"testing"
)

// TestDerivedActivity checks the local feed: comments carry their body and
// author, a ticket appears once as created, and a ticket touched after it was
// made also appears as updated.
func TestDerivedActivity(t *testing.T) {
	d := openTestDB(t)
	creator := "jrdn"
	tsk, err := Create(d, "feed task", CreateOpts{CreatedBy: &creator})
	if err != nil {
		t.Fatal(err)
	}
	author := "hermes"
	if _, err := AddComment(d, tsk.ID, "some progress", &author); err != nil {
		t.Fatal(err)
	}
	// Pin the update time: a same-second create and update would compare
	// equal and look like a create-only ticket.
	if _, err := d.Exec(d.Rebind(`UPDATE tasks SET updated_at = ? WHERE id = ?`),
		"2099-01-01T00:00:00Z", tsk.ID); err != nil {
		t.Fatal(err)
	}

	acts, err := DerivedActivity(d, 20)
	if err != nil {
		t.Fatalf("DerivedActivity: %v", err)
	}
	if len(acts) != 3 {
		t.Fatalf("got %d entries, want created + updated + comment: %+v", len(acts), acts)
	}
	if acts[0].Kind != ActivityUpdated || acts[0].TaskID != tsk.ID || acts[0].TaskTitle != "feed task" {
		t.Errorf("first entry = %+v, want the pinned update", acts[0])
	}
	kinds := map[string]Activity{}
	for _, a := range acts {
		kinds[a.Kind] = a
	}
	created, ok := kinds[ActivityCreated]
	if !ok || created.Actor == nil || *created.Actor != creator {
		t.Errorf("created entry = %+v", created)
	}
	comment, ok := kinds[ActivityComment]
	if !ok || comment.Body != "some progress" || comment.Actor == nil || *comment.Actor != "hermes" {
		t.Errorf("comment entry = %+v", comment)
	}
	if comment.TaskTitle != "feed task" {
		t.Errorf("comment lacks its ticket title: %+v", comment)
	}

	// The limit trims from the newest end.
	one, err := DerivedActivity(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].Kind != ActivityUpdated {
		t.Errorf("limited feed = %+v, want just the newest entry", one)
	}
}
