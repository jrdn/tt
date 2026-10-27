package cmd

import (
	"testing"

	"github.com/jrdn/tt/internal/client"
	"github.com/jrdn/tt/internal/task"
)

// nopStore stands in for the store the root command opened; the resolve tests
// only care which store comes back, not what it does.
type nopStore struct{ task.Store }

// A task reference may name its project before the ID, separated by a dot
// (what tt prints, e.g. tt.1hde6nf) or a colon (what people type).
func TestSplitTaskRef(t *testing.T) {
	for _, tc := range []struct{ in, qualifier, id string }{
		{"1hde6nf", "", "1hde6nf"},
		{"tt.1hde6nf", "tt", "1hde6nf"},
		{"tt:1hde6nf", "tt", "1hde6nf"},
		{"life.1hde6nf", "life", "1hde6nf"},
		// Nothing to split: a bare ID never starts or ends with a separator.
		{".1hde6nf", "", ".1hde6nf"},
		{"1hde6nf.", "", "1hde6nf."},
	} {
		qual, id := splitTaskRef(tc.in)
		if qual != tc.qualifier || id != tc.id {
			t.Errorf("splitTaskRef(%q) = %q, %q; want %q, %q", tc.in, qual, id, tc.qualifier, tc.id)
		}
	}
}

// On a tt server, a prefix naming the current project is dropped and the task
// resolves in the store the root command opened; a prefix naming another
// project resolves through a store for that project instead of the local
// database path.
func TestResolveTaskProjectPrefixOnServer(t *testing.T) {
	current := &nopStore{}
	oldStore, oldRemote := store, remote
	t.Cleanup(func() { store, remote = oldStore, oldRemote })
	store, remote = current, &client.ProjectConfig{Server: "http://tt.example", Project: "tt"}
	t.Setenv("TT_API_KEY", "test-key")

	for _, raw := range []string{"tt.1hde6nf", "tt:1hde6nf"} {
		qt, err := resolveTask(raw)
		if err != nil {
			t.Fatalf("resolveTask(%q): %v", raw, err)
		}
		if qt.id != "1hde6nf" {
			t.Errorf("resolveTask(%q).id = %q, want 1hde6nf", raw, qt.id)
		}
		if qt.store != task.Store(current) || qt.opened {
			t.Errorf("resolveTask(%q) did not reuse the current project's store", raw)
		}
	}

	qt, err := resolveTask("puck.5qvhema")
	if err != nil {
		t.Fatalf("resolveTask(puck.5qvhema): %v", err)
	}
	defer qt.Close()
	if qt.id != "5qvhema" {
		t.Errorf("id = %q, want 5qvhema", qt.id)
	}
	if _, ok := qt.store.(*client.Store); !ok {
		t.Errorf("store = %T, want a client store for the named project", qt.store)
	}
}

// Without a server, the prefix still names a database (the older behaviour).
func TestResolveTaskPrefixWithoutServer(t *testing.T) {
	current := &nopStore{}
	oldStore, oldRemote := store, remote
	t.Cleanup(func() { store, remote = oldStore, oldRemote })
	store, remote = current, nil
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// An unqualified ID keeps using the current store.
	qt, err := resolveTask("1hde6nf")
	if err != nil || qt.id != "1hde6nf" || qt.store != task.Store(current) {
		t.Fatalf("unqualified resolveTask = %+v, %v", qt, err)
	}

	// A qualified ID opens its own database and hands back the bare ID, so the
	// caller's store calls work unchanged.
	qt, err = resolveTask("somedb:1hde6nf")
	if err != nil {
		t.Fatalf("resolveTask(somedb:1hde6nf): %v", err)
	}
	defer qt.Close()
	if qt.id != "1hde6nf" || !qt.opened || qt.store == task.Store(current) {
		t.Fatalf("qualified resolveTask = %+v", qt)
	}
}
