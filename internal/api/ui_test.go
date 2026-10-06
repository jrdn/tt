//go:build ui

package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/jrdn/tt/internal/api"
	"github.com/jrdn/tt/internal/db"
	"github.com/jrdn/tt/internal/task"
)

const (
	lightBG = "rgb(243, 244, 246)"
	darkBG  = "rgb(24, 24, 37)"
)

// newBrowser serves the web UI from a temp SQLite DB and returns a headless
// Chrome context, skipping the test when Chrome isn't available.
func newBrowser(t *testing.T) (context.Context, string) {
	t.Helper()
	d, err := db.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	store := task.NewSQLStore(d)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", api.Index)
	(&api.Server{Resolve: func(*http.Request) (task.Store, error) { return store, nil }}).Register(mux, "/api")
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	alloc, cancelAlloc := chromedp.NewExecAllocator(context.Background(), append(chromedp.DefaultExecAllocatorOptions[:], chromedp.Headless)...)
	t.Cleanup(cancelAlloc)
	ctx, cancel := chromedp.NewContext(alloc)
	t.Cleanup(cancel)
	ctx, cancelTimeout := context.WithTimeout(ctx, 30*time.Second)
	t.Cleanup(cancelTimeout)
	if err := chromedp.Run(ctx); err != nil {
		t.Skipf("Chrome not available: %v", err)
	}
	return ctx, ts.URL
}

func systemTheme(scheme string) chromedp.Action {
	return emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{{Name: "prefers-color-scheme", Value: scheme}})
}

func bodyBG(t *testing.T, ctx context.Context) string {
	t.Helper()
	var bg string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`getComputedStyle(document.body).backgroundColor`, &bg)); err != nil {
		t.Fatal(err)
	}
	return bg
}

func btnText(t *testing.T, ctx context.Context) string {
	t.Helper()
	var s string
	if err := chromedp.Run(ctx, chromedp.Text("#theme-btn", &s, chromedp.ByID)); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestThemeFollowsSystem(t *testing.T) {
	ctx, url := newBrowser(t)
	for scheme, want := range map[string]string{"light": lightBG, "dark": darkBG} {
		if err := chromedp.Run(ctx, systemTheme(scheme), chromedp.Navigate(url), chromedp.WaitVisible("#theme-btn")); err != nil {
			t.Fatal(err)
		}
		if got := bodyBG(t, ctx); got != want {
			t.Errorf("system %s: body background = %s, want %s", scheme, got, want)
		}
		if got := btnText(t, ctx); got != "◐ Auto" {
			t.Errorf("system %s: button = %q, want Auto", scheme, got)
		}
	}
}

func TestThemeToggleOverridesSystem(t *testing.T) {
	ctx, url := newBrowser(t)
	if err := chromedp.Run(ctx, systemTheme("dark"), chromedp.Navigate(url), chromedp.WaitVisible("#theme-btn")); err != nil {
		t.Fatal(err)
	}

	// Auto -> Light: overrides the dark system theme.
	if err := chromedp.Run(ctx, chromedp.Click("#theme-btn", chromedp.ByID)); err != nil {
		t.Fatal(err)
	}
	if got := bodyBG(t, ctx); got != lightBG {
		t.Errorf("after Light: body background = %s, want %s", got, lightBG)
	}
	if got := btnText(t, ctx); got != "☀ Light" {
		t.Errorf("button = %q, want Light", got)
	}

	// The override survives a reload.
	if err := chromedp.Run(ctx, chromedp.Reload(), chromedp.WaitVisible("#theme-btn")); err != nil {
		t.Fatal(err)
	}
	if got := bodyBG(t, ctx); got != lightBG {
		t.Errorf("after reload: body background = %s, want %s", got, lightBG)
	}

	// Light -> Dark.
	if err := chromedp.Run(ctx, chromedp.Click("#theme-btn", chromedp.ByID)); err != nil {
		t.Fatal(err)
	}
	if got := bodyBG(t, ctx); got != darkBG {
		t.Errorf("after Dark: body background = %s, want %s", got, darkBG)
	}

	// Dark -> Auto: back to following the system, and the stored choice is cleared.
	if err := chromedp.Run(ctx, systemTheme("light"), chromedp.Click("#theme-btn", chromedp.ByID)); err != nil {
		t.Fatal(err)
	}
	if got := bodyBG(t, ctx); got != lightBG {
		t.Errorf("after Auto with light system: body background = %s, want %s", got, lightBG)
	}
	var stored any
	if err := chromedp.Run(ctx, chromedp.Evaluate(`localStorage.getItem('tt-theme')`, &stored)); err != nil {
		t.Fatal(err)
	}
	if stored != nil {
		t.Errorf("tt-theme = %v after returning to Auto, want unset", stored)
	}
}

func TestDashboardShowsStatusCounts(t *testing.T) {
	ctx, url := newBrowser(t)
	if err := chromedp.Run(ctx, chromedp.Navigate(url), chromedp.WaitVisible("#dash-btn")); err != nil {
		t.Fatal(err)
	}
	var created bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`fetch('/api/tasks', {method:'POST', body: JSON.stringify({title:'dash task', status:'in_progress'})}).then(r => r.ok)`, &created, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) })); err != nil || !created {
		t.Fatalf("create task: %v %v", created, err)
	}
	var text string
	if err := chromedp.Run(ctx,
		chromedp.Click("#dash-btn", chromedp.ByID),
		chromedp.Poll(`document.querySelector('.dash-card') !== null`, nil),
		chromedp.Evaluate(`document.getElementById('dashboard').innerText`, &text),
	); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "In Progress 1") {
		t.Errorf("dashboard text = %q, want in-progress count", text)
	}
}

// newServerModeBrowser serves the server-mode UI with two projects, "alpha"
// and "beta", each with one task.
func newServerModeBrowser(t *testing.T) (context.Context, string) {
	t.Helper()
	stores := map[string]task.Store{}
	for _, slug := range []string{"alpha", "beta"} {
		d, err := db.OpenPath(filepath.Join(t.TempDir(), slug+".db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { d.Close() })
		st := task.NewSQLStore(d)
		who := "bot-" + slug
		if _, err := st.Create(context.Background(), slug+" task", task.CreateOpts{Assignee: &who}); err != nil {
			t.Fatal(err)
		}
		stores[slug] = st
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", api.ServerIndex)
	mux.HandleFunc("POST /api/v1/token", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"token":"x"}`)) })
	mux.HandleFunc("GET /api/v1/whoami", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"handle":"jrdn"}`))
	})
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"projects":[{"slug":"alpha","name":"Alpha"},{"slug":"beta","name":"Beta"}]}`))
	})
	(&api.Server{Resolve: func(r *http.Request) (task.Store, error) { return stores[r.PathValue("project")], nil }}).Register(mux, "/api/v1/projects/{project}")
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	ctx, _ := newBrowser(t)
	return ctx, ts.URL
}

func TestProjectSwitcherIsStickyAndAllShowsProjects(t *testing.T) {
	ctx, url := newServerModeBrowser(t)
	var sel, list string
	if err := chromedp.Run(ctx, chromedp.Navigate(url), chromedp.Poll(`document.querySelector('#project-select option') !== null`, nil),
		chromedp.Evaluate(`switchProject('beta')`, nil), // reloads the page
		chromedp.Sleep(500*time.Millisecond),
		chromedp.Poll(`document.querySelector('.task-item') !== null`, nil),
	); err != nil {
		t.Fatal(err)
	}
	// A fresh visit without ?project= keeps the last choice.
	if err := chromedp.Run(ctx, chromedp.Navigate(url), chromedp.Poll(`document.querySelector('.task-item') !== null`, nil),
		chromedp.Value("#project-select", &sel, chromedp.ByID)); err != nil {
		t.Fatal(err)
	}
	if sel != "beta" {
		t.Errorf("project after revisit = %q, want beta", sel)
	}

	if err := chromedp.Run(ctx, chromedp.Evaluate(`switchProject('all')`, nil), chromedp.Sleep(500*time.Millisecond), chromedp.Poll(`document.querySelector('.project-chip') !== null`, nil),
		chromedp.Evaluate(`document.getElementById('task-list').innerText`, &list)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"alpha task", "beta task", "Alpha", "Beta", "@bot-alpha", "@bot-beta"} {
		if !strings.Contains(list, want) {
			t.Errorf("all-projects list missing %q: %q", want, list)
		}
	}
}

// TestServerModeCommentBoxShowsLoggedInUser verifies that on tt server the
// add-comment box credits the logged-in handle and doesn't ask for an author.
func TestServerModeCommentBoxShowsLoggedInUser(t *testing.T) {
	ctx, url := newServerModeBrowser(t)
	var footer string
	var noInput bool
	// Clicks go through Evaluate: the live-refresh stream re-renders the
	// task list, which invalidates the DOM nodes chromedp.Click waits on.
	if err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.Poll(`document.querySelector('.task-item') !== null`, nil),
		chromedp.Evaluate(`document.querySelector('.task-item').click()`, nil),
		// hydrateCommentIdentity swaps in the handle once whoami answers.
		chromedp.Poll(`document.querySelector('.author-name') !== null`, nil),
		chromedp.Evaluate(`document.querySelector('.add-comment-footer').innerText`, &footer),
		chromedp.Evaluate(`document.getElementById('comment-author') === null`, &noInput),
		// Submitting through the real UI must still work without the input.
		chromedp.Evaluate(`document.getElementById('comment-body').value = 'hi'; document.querySelector('.add-comment-footer .btn-primary').click()`, nil),
		chromedp.Poll(`document.querySelector('.comment-card') !== null`, nil),
	); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(footer, "jrdn") {
		t.Errorf("comment footer = %q, want the logged-in handle jrdn", footer)
	}
	if !noInput {
		t.Errorf("author input shown in server mode, want hidden")
	}
}

// TestLocalModeCommentBoxStillAsks checks local tt web keeps the author field.
func TestLocalModeCommentBoxStillAsks(t *testing.T) {
	ctx, url := newBrowser(t)
	var footer string
	if err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.WaitVisible("#task-list"),
		chromedp.Evaluate(`fetch('/api/tasks', {method:'POST', body: JSON.stringify({title:'c task'})}).then(r => r.ok)`, nil,
			func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }),
		chromedp.Evaluate(`loadTasks()`, nil),
		chromedp.Poll(`document.querySelector('.task-item') !== null`, nil),
		chromedp.Evaluate(`document.querySelector('.task-item').click()`, nil),
		chromedp.Poll(`document.querySelector('#comment-body') !== null`, nil),
		chromedp.Evaluate(`document.querySelector('.add-comment-footer').innerText`, &footer),
	); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(footer, "Author:") {
		t.Errorf("local-mode comment footer = %q, want an Author prompt", footer)
	}
	var hasInput bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.getElementById('comment-author') !== null`, &hasInput)); err != nil {
		t.Fatal(err)
	}
	if !hasInput {
		t.Errorf("local-mode author input missing, want it present")
	}
}
