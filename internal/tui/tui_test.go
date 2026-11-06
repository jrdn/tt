package tui_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jrdn/tt/internal/db"
	"github.com/jrdn/tt/internal/task"
	"github.com/jrdn/tt/internal/tui"
)

// driver feeds messages and keys to a tui.Model the way the bubbletea
// runtime would, running the commands it returns.
type driver struct {
	t *testing.T
	m tea.Model
}

func newDriver(t *testing.T, s task.Store) *driver {
	d := &driver{t: t, m: tui.NewModel(s)}
	d.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	d.settle(d.m.Init())
	return d
}

func (d *driver) send(msg tea.Msg) {
	var cmd tea.Cmd
	d.m, cmd = d.m.Update(msg)
	d.settle(cmd)
}

// settle runs cmd and feeds its result back. A command that doesn't return
// promptly is the live-update wait, which blocks until the next change.
func (d *driver) settle(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	out := make(chan tea.Msg, 1)
	go func() { out <- cmd() }()
	select {
	case msg := <-out:
		switch msg := msg.(type) {
		case nil:
		case tea.BatchMsg:
			for _, c := range msg {
				d.settle(c)
			}
		default:
			d.send(msg)
		}
	case <-time.After(300 * time.Millisecond):
	}
}

func (d *driver) key(keys ...string) {
	for _, k := range keys {
		switch k {
		case "enter":
			d.send(tea.KeyMsg{Type: tea.KeyEnter})
		case "esc":
			d.send(tea.KeyMsg{Type: tea.KeyEsc})
		case "down":
			d.send(tea.KeyMsg{Type: tea.KeyDown})
		default:
			d.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		}
	}
}

func (d *driver) typeText(s string) {
	for _, r := range s {
		d.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func (d *driver) view() string { return d.m.View() }

func (d *driver) wantView(substr string) {
	d.t.Helper()
	if v := d.view(); !strings.Contains(v, substr) {
		d.t.Errorf("view lacks %q:\n%s", substr, v)
	}
}

// runTUI drives the TUI against a store: it lists, opens, comments on,
// claims, changes the status of, completes and creates tasks, then checks
// the store itself changed. The same run covers a local and a server store.
func runTUI(t *testing.T, s task.Store) {
	ctx := context.Background()
	alpha, err := s.Create(ctx, "Alpha task", task.CreateOpts{Priority: 1, PrioritySet: true})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := s.Create(ctx, "Beta task", task.CreateOpts{Priority: 2, PrioritySet: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddRelation(ctx, alpha.ID, "blocks", beta.ID); err != nil {
		t.Fatal(err)
	}

	d := newDriver(t, s)
	d.wantView("Alpha task")
	d.wantView("Beta task")

	status := func(id string) task.Status {
		t.Helper()
		got, err := s.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return got.Status
	}

	// Open the first row's detail, with its comments and relations.
	var first string
	d.key("j", "enter") // the cursor starts on the section header
	if !strings.Contains(d.view(), "j/k scroll") {
		t.Fatalf("enter didn't open a task detail:\n%s", d.view())
	}
	for _, tk := range []*task.Task{alpha, beta} {
		if strings.Contains(d.view(), tk.ID+" "+tk.Title) {
			first = tk.ID
		}
	}
	if first == "" {
		t.Fatalf("enter didn't open a task detail:\n%s", d.view())
	}

	// Comment on it from the detail view.
	d.key("m")
	d.typeText("from the tui")
	d.key("enter")
	comments, err := s.GetComments(ctx, first)
	if err != nil || len(comments) != 1 || comments[0].Body != "from the tui" {
		t.Errorf("comments after m = %v, %v", comments, err)
	}
	d.key("g") // refresh the detail
	d.wantView("from the tui")

	// Claim, then pick a status, then mark done.
	d.key("C")
	if got := status(first); got != task.StatusInProgress {
		t.Errorf("status after C = %q", got)
	}
	d.key("S", "down", "down", "enter") // open, backlog, ready
	if got := status(first); got != task.StatusReady {
		t.Errorf("status after S = %q", got)
	}
	d.key("x")
	if got := status(first); got != task.StatusDone {
		t.Errorf("status after x = %q", got)
	}

	// Back in the list, create a task and see it.
	d.key("q")
	d.key("c")
	d.typeText("Created in the tui")
	d.key("enter")
	all, err := s.List(ctx, task.ListOpts{All: true})
	if err != nil {
		t.Fatal(err)
	}
	var created bool
	for _, tk := range all {
		created = created || tk.Title == "Created in the tui"
	}
	if !created {
		t.Errorf("c didn't create a task; store has %v", all)
	}
	d.key("g")
	d.wantView("Created in the tui")

	// Done tasks are hidden until toggled; the graph view renders.
	d.key("a")
	d.key("v")
	d.wantView("graph")
	d.key("q")
	d.key("?")
	d.wantView("Keybindings")
	if strings.Contains(d.view(), "Error:") {
		t.Errorf("TUI reported an error:\n%s", d.view())
	}
}

func TestTUI_LocalStore(t *testing.T) {
	database, err := db.OpenPath(filepath.Join(t.TempDir(), "tui.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	runTUI(t, task.NewSQLStore(database))
}
