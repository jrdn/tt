//go:build ui

package api_test

import (
	"context"
	"encoding/json"
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

// TestDashboardShowsActivityFeed checks the dashboard renders the feed: the
// ticket, what happened, and a comment's text.
func TestDashboardShowsActivityFeed(t *testing.T) {
	ctx, url := newBrowser(t)
	if err := chromedp.Run(ctx, chromedp.Navigate(url), chromedp.WaitVisible("#dash-btn")); err != nil {
		t.Fatal(err)
	}
	await := func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }
	var id string
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`fetch('/api/tasks', {method:'POST', body: JSON.stringify({title:'feed task'})}).then(r => r.json()).then(t => t.id)`,
		&id, await)); err != nil {
		t.Fatalf("create task: %v", err)
	}
	var commented bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`fetch('/api/tasks/`+id+`/comments', {method:'POST', body: JSON.stringify({body:'looked into it', author:'jrdn'})}).then(r => r.ok)`,
		&commented, await)); err != nil || !commented {
		t.Fatalf("comment: %v %v", commented, err)
	}
	var text string
	if err := chromedp.Run(ctx,
		chromedp.Click("#dash-btn", chromedp.ByID),
		chromedp.Poll(`document.querySelectorAll('#dash-activity-rows .activity-row').length >= 2`, nil),
		chromedp.Evaluate(`document.getElementById('dash-activity-rows').innerText`, &text),
	); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"feed task", "COMMENT", "looked into it", "@jrdn"} {
		if !strings.Contains(text, want) {
			t.Errorf("activity feed missing %q: %q", want, text)
		}
	}
}

func TestDashboardIssueTableFilters(t *testing.T) {
	ctx, url := newBrowser(t)
	if err := chromedp.Run(ctx, chromedp.Navigate(url), chromedp.WaitVisible("#dash-btn")); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{title:'apple pie', status:'open', assignee:'ann'}`,
		`{title:'banana split', status:'in_progress', assignee:'bob'}`,
	} {
		var ok bool
		if err := chromedp.Run(ctx, chromedp.Evaluate(`fetch('/api/tasks', {method:'POST', body: JSON.stringify(`+body+`)}).then(r => r.ok)`, &ok, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) })); err != nil || !ok {
			t.Fatalf("create task: %v %v", ok, err)
		}
	}
	rows := func(js string) string {
		var out string
		if err := chromedp.Run(ctx, chromedp.Evaluate(js+`; new Promise(r => setTimeout(() => r(document.getElementById('issue-rows').innerText), 400))`, &out, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) })); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if err := chromedp.Run(ctx, chromedp.Click("#dash-btn", chromedp.ByID), chromedp.Poll(`document.querySelectorAll('#issue-rows tr').length >= 2`, nil)); err != nil {
		t.Fatal(err)
	}
	if got := rows(`0`); !strings.Contains(got, "apple pie") || !strings.Contains(got, "banana split") {
		t.Errorf("unfiltered rows = %q", got)
	}
	if got := rows(`document.getElementById('issue-search').value='banana'; onIssueSearch()`); strings.Contains(got, "apple") || !strings.Contains(got, "banana") {
		t.Errorf("search rows = %q", got)
	}
	if got := rows(`document.getElementById('issue-search').value=''; document.getElementById('issue-assignee').value='ann'; renderIssues()`); strings.Contains(got, "banana") || !strings.Contains(got, "apple") {
		t.Errorf("assignee rows = %q", got)
	}
	if got := rows(`document.getElementById('issue-assignee').value=''; document.getElementById('issue-status').value='in_progress'; renderIssues()`); strings.Contains(got, "apple") || !strings.Contains(got, "banana") {
		t.Errorf("status rows = %q", got)
	}
}

// TestDashboardIssueOpensInAllProjectsMode covers clicking a ticket in the
// dashboard's list while the project selector is on "All projects": the ticket
// must open, not reload into an empty detail view.
func TestDashboardIssueOpensInAllProjectsMode(t *testing.T) {
	ctx, url := newServerModeBrowser(t)
	if err := chromedp.Run(ctx,
		chromedp.Navigate(url+"/?project=all"),
		chromedp.Poll(`document.querySelector('#project-select') !== null`, nil),
		chromedp.Sleep(300*time.Millisecond),
		chromedp.Click("#dash-btn", chromedp.ByID),
		chromedp.Poll(`document.querySelectorAll('#issue-rows tr').length >= 2`, nil),
	); err != nil {
		t.Fatal(err)
	}
	var search, title string
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`[...document.querySelectorAll('#issue-rows tr')].find(tr => tr.innerText.includes('beta task')).click()`, nil),
		chromedp.Poll(`document.getElementById('detail-title') !== null`, nil),
		chromedp.Evaluate(`document.getElementById('detail-title').innerText`, &title),
		chromedp.Evaluate(`location.search`, &search),
	); err != nil {
		t.Fatal(err)
	}
	if title != "beta task" {
		t.Errorf("detail title = %q, want beta task", title)
	}
	if !strings.Contains(search, "task=") || !strings.Contains(search, "project=beta") {
		t.Errorf("location.search = %q, want the task and its project", search)
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
		tsk, err := st.Create(context.Background(), slug+" task", task.CreateOpts{Assignee: &who})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.AddComment(context.Background(), tsk.ID, "working on it", &who); err != nil {
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

// TestRecentPopupShowsProjectAndAssignee checks the recent list names the
// project and assignee of each task, not just its title.
func TestRecentPopupShowsProjectAndAssignee(t *testing.T) {
	ctx, url := newServerModeBrowser(t)
	var rows string
	if err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.Poll(`document.querySelector('.task-item') !== null`, nil),
		chromedp.Evaluate(`toggleRecent()`, nil),
		chromedp.Poll(`document.querySelector('.recent-row') !== null`, nil),
		chromedp.Evaluate(`document.getElementById('recent-list').innerText`, &rows),
	); err != nil {
		t.Fatal(err)
	}
	// The popup is the activity feed: what happened, on which ticket, in
	// which project, by whom.
	for _, want := range []string{"alpha task", "Alpha", "@bot-alpha", "COMMENT", "CREATED"} {
		if !strings.Contains(rows, want) {
			t.Errorf("recent activity missing %q: %q", want, rows)
		}
	}
}

// TestNewTaskDescriptionIsTall checks the new-ticket description box opens a
// few lines tall instead of the old one-line stub.
func TestNewTaskDescriptionIsTall(t *testing.T) {
	ctx, url := newBrowser(t)
	var height float64
	if err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.WaitVisible(`button[onclick="openNewTask()"]`),
		chromedp.Evaluate(`openNewTask()`, nil),
		chromedp.Poll(`document.getElementById('new-task-overlay').classList.contains('open')`, nil),
		chromedp.Evaluate(`document.getElementById('nt-desc').getBoundingClientRect().height`, &height),
	); err != nil {
		t.Fatal(err)
	}
	if height < 140 {
		t.Errorf("description box height = %.0fpx, want at least 140px", height)
	}
}

// TestNewTaskModalIsRoomy checks the new-ticket popup is wider than the old
// cramped 500px card and never taller than the viewport.
func TestNewTaskModalIsRoomy(t *testing.T) {
	ctx, url := newBrowser(t)
	var width, height float64
	var vh float64
	if err := chromedp.Run(ctx,
		emulation.SetDeviceMetricsOverride(1280, 800, 1, false),
		chromedp.Navigate(url),
		chromedp.WaitVisible(`button[onclick="openNewTask()"]`),
		chromedp.Evaluate(`openNewTask()`, nil),
		chromedp.Poll(`document.getElementById('new-task-overlay').classList.contains('open')`, nil),
		chromedp.Evaluate(`document.querySelector('.modal').getBoundingClientRect().width`, &width),
		chromedp.Evaluate(`document.querySelector('.modal').getBoundingClientRect().height`, &height),
		chromedp.Evaluate(`window.innerHeight`, &vh),
	); err != nil {
		t.Fatal(err)
	}
	if width < 640 {
		t.Errorf("new task modal width = %.0fpx, want at least 640px", width)
	}
	if height > vh {
		t.Errorf("new task modal height = %.0fpx, want it to fit in the %.0fpx viewport", height, vh)
	}
}

// TestSidebarSplitterResizes checks the divider between the task list and the
// detail pane can be dragged, clamps sensibly, and remembers the width.
func TestSidebarSplitterResizes(t *testing.T) {
	ctx, url := newBrowser(t)
	var before, after float64
	var stored string
	if err := chromedp.Run(ctx,
		emulation.SetDeviceMetricsOverride(1280, 800, 1, false),
		chromedp.Navigate(url),
		chromedp.WaitVisible("#splitter"),
		chromedp.Evaluate(`(localStorage.removeItem('tt-sidebar-width'), document.querySelector('.layout aside').getBoundingClientRect().width)`, &before),
		chromedp.Evaluate(`(() => {
			const bar = document.getElementById('splitter');
			const aside = document.querySelector('.layout aside');
			const left = aside.getBoundingClientRect().left;
			bar.dispatchEvent(new PointerEvent('pointerdown', {bubbles: true, clientX: left + aside.getBoundingClientRect().width}));
			window.dispatchEvent(new PointerEvent('pointermove', {bubbles: true, clientX: left + 480}));
			window.dispatchEvent(new PointerEvent('pointerup', {bubbles: true}));
		})()`, nil),
		chromedp.Evaluate(`document.querySelector('.layout aside').getBoundingClientRect().width`, &after),
		chromedp.Evaluate(`localStorage.getItem('tt-sidebar-width') || ''`, &stored),
	); err != nil {
		t.Fatal(err)
	}
	if after < before+100 {
		t.Errorf("sidebar width after dragging = %.0fpx, want it wider than the %.0fpx default", after, before)
	}
	if after > 640 {
		t.Errorf("sidebar width after dragging = %.0fpx, want it clamped at 640px", after)
	}
	if stored == "" {
		t.Error("sidebar width was not remembered in localStorage")
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

// TestStatusControlReadsAsDropdown checks the detail header's status control
// centres its label, shows a glyph for the current status, and carries a
// caret so it looks like a dropdown rather than a button.
func TestStatusControlReadsAsDropdown(t *testing.T) {
	ctx, url := newBrowser(t)
	var align, icon, caret string
	var box []float64
	if err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.WaitVisible("#task-list"),
		chromedp.Evaluate(`fetch('/api/tasks', {method:'POST', body: JSON.stringify({title:'status task', status:'in_progress'})}).then(r => r.ok)`, nil,
			func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }),
		chromedp.Evaluate(`loadTasks()`, nil),
		chromedp.Poll(`document.querySelector('.task-item') !== null`, nil),
		chromedp.Evaluate(`document.querySelector('.task-item').click()`, nil),
		chromedp.Poll(`document.querySelector('#status-sel') !== null`, nil),
		chromedp.Evaluate(`getComputedStyle(document.getElementById('status-sel')).textAlign`, &align),
		chromedp.Evaluate(`document.getElementById('status-icon').textContent`, &icon),
		chromedp.Evaluate(`document.querySelector('.status-caret').textContent`, &caret),
		// glyph right edge, caret left edge, the select's centre, left edge, width
		chromedp.Evaluate(`(() => {
			const sel = document.getElementById('status-sel').getBoundingClientRect();
			const ic  = document.getElementById('status-icon').getBoundingClientRect();
			const ca  = document.querySelector('.status-caret').getBoundingClientRect();
			return [ic.right, ca.left, sel.left + sel.width / 2, sel.left, sel.width];
		})()`, &box),
	); err != nil {
		t.Fatal(err)
	}
	if len(box) != 5 {
		t.Fatalf("geometry = %v, want 5 numbers", box)
	}
	iconRight, caretLeft, centre, selectLeft, selectWidth := box[0], box[1], box[2], box[3], box[4]

	if align != "center" {
		t.Errorf("status select text-align = %q, want center", align)
	}
	if icon != "◐" {
		t.Errorf("status glyph = %q, want ◐ for in_progress", icon)
	}
	if caret != "▾" {
		t.Errorf("caret = %q, want a down arrow", caret)
	}
	if iconRight < selectLeft+2 || iconRight > centre {
		t.Errorf("glyph right edge = %.0f, want it inside the control left of centre (%.0f..%.0f)", iconRight, selectLeft, centre)
	}
	if caretLeft < centre || caretLeft > selectLeft+selectWidth-2 {
		t.Errorf("caret left edge = %.0f, want it right of centre (%.0f) and inside the control", caretLeft, centre)
	}
}

// TestCommentMarkdownIsRenderedAndStaysInItsCard checks the two halves of the
// comment formatting ticket: markdown (including GFM tables, strikethrough and
// bare-URL links) renders in comments, and a long URL wraps inside the comment
// card instead of widening the detail pane.
func TestCommentMarkdownIsRenderedAndStaysInItsCard(t *testing.T) {
	ctx, url := newBrowser(t)
	longURL := "https://example.com/" + strings.Repeat("a-very-long-path-segment/", 12)
	desc := "## Plan\n\n| step | who |\n| ---- | --- |\n| ship | me  |"
	body := "look at " + longURL + "\n\n- [x] done\n\n~~old~~ **new**"

	var table, del, strong, bulet, target string
	var overflow []float64
	if err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.WaitVisible("#task-list"),
		chromedp.Evaluate(`fetch('/api/tasks', {method:'POST', body: JSON.stringify({title:'md task', description:`+
			jsString(desc)+`})}).then(r => r.json())`, nil,
			func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }),
		chromedp.Evaluate(`loadTasks()`, nil),
		chromedp.Poll(`document.querySelector('.task-item') !== null`, nil),
		chromedp.Evaluate(`document.querySelector('.task-item').click()`, nil),
		chromedp.Poll(`document.querySelector('#comment-body') !== null`, nil),
		chromedp.Evaluate(`(() => {
			const id = currentTaskData.task.id;
			return fetch('/api/tasks/' + encodeURIComponent(id) + '/comments', {
				method: 'POST',
				body: JSON.stringify({body: `+jsString(body)+`, author: 'tester'})
			}).then(r => r.ok);
		})()`, nil, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }),
		chromedp.Evaluate(`selectTask(currentTaskData.task.id)`, nil),
		chromedp.Poll(`document.querySelector('.comment-body del') !== null`, nil),
		chromedp.Evaluate(`document.querySelector('.md-body table') !== null ? 'yes' : 'no'`, &table),
		chromedp.Evaluate(`document.querySelector('.comment-body del').textContent`, &del),
		chromedp.Evaluate(`document.querySelector('.comment-body strong').textContent`, &strong),
		chromedp.Evaluate(`document.querySelector('.comment-body li input[type=checkbox]') !== null ? 'yes' : 'no'`, &bulet),
		chromedp.Evaluate(`document.querySelector('.comment-body a').target`, &target),
		chromedp.Evaluate(`(() => {
			const card = document.querySelector('.comment-card').getBoundingClientRect();
			const link = document.querySelector('.comment-body a').getBoundingClientRect();
			return [card.right, link.right, document.documentElement.scrollWidth, document.documentElement.clientWidth];
		})()`, &overflow),
	); err != nil {
		t.Fatal(err)
	}
	if table != "yes" {
		t.Error("description table did not render inside .md-body")
	}
	if del != "old" {
		t.Errorf("strikethrough in a comment rendered as %q, want old", del)
	}
	if strong != "new" {
		t.Errorf("bold in a comment rendered as %q, want new", strong)
	}
	if bulet != "yes" {
		t.Error("task list checkbox missing from the comment")
	}
	if target != "_blank" {
		t.Errorf("comment link target = %q, want _blank so the UI keeps its state", target)
	}
	if len(overflow) != 4 {
		t.Fatalf("geometry = %v, want 4 numbers", overflow)
	}
	cardRight, linkRight, scrollWidth, clientWidth := overflow[0], overflow[1], overflow[2], overflow[3]
	if linkRight > cardRight+1 {
		t.Errorf("long URL runs %.0fpx past the comment card edge", linkRight-cardRight)
	}
	if scrollWidth > clientWidth+1 {
		t.Errorf("page scrolls horizontally: scrollWidth %.0f > clientWidth %.0f", scrollWidth, clientWidth)
	}
}

// jsString quotes s as a JavaScript string literal.
func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestTaskIdsShowTheirProject checks the server UI prefixes every task ID with
// its project, so a bare four-char hash can't be read as another project's
// ticket. The list, the detail panel and the all-projects view all agree.
func TestTaskIdsShowTheirProject(t *testing.T) {
	ctx, url := newServerModeBrowser(t)
	var listChip, detailID, allChips string
	var active bool
	if err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.Poll(`document.querySelector('.task-item .id-chip') !== null`, nil),
		chromedp.Evaluate(`document.querySelector('.task-item .id-chip').textContent.trim()`, &listChip),
		// Selecting a task must not depend on the chip text matching the raw ID.
		chromedp.Evaluate(`document.querySelector('.task-item').click()`, nil),
		chromedp.Poll(`document.querySelector('.meta-grid .meta-value') !== null`, nil),
		chromedp.Evaluate(`document.querySelector('.meta-grid .meta-value').textContent.trim()`, &detailID),
		chromedp.Evaluate(`document.querySelector('.task-item').classList.contains('active')`, &active),
	); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(listChip, "alpha.") {
		t.Errorf("list id chip = %q, want an alpha. prefix", listChip)
	}
	if detailID != listChip {
		t.Errorf("detail id = %q, list id = %q; want the same qualified ID", detailID, listChip)
	}
	if !active {
		t.Errorf("clicking a task did not mark it active")
	}

	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`switchProject('all')`, nil),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.Poll(`document.querySelector('.project-chip') !== null`, nil),
		chromedp.Evaluate(`[...document.querySelectorAll('.task-item .id-chip')].map(e => e.textContent.trim()).join(' ')`, &allChips),
	); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"alpha.", "beta."} {
		if !strings.Contains(allChips, want) {
			t.Errorf("all-projects ids %q missing a %s prefix", allChips, want)
		}
	}
}

// TestTaskDeepLink checks a task link opens straight into the task, that the
// address bar follows what is on screen, and that leaving the task clears it.
func TestTaskDeepLink(t *testing.T) {
	ctx, url := newBrowser(t)
	if err := chromedp.Run(ctx, chromedp.Navigate(url), chromedp.WaitVisible("#dash-btn")); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`fetch('/api/tasks', {method:'POST', body: JSON.stringify({title:'deep link me'})})
		.then(r => r.json()).then(d => (d.task || d).id)`, &id,
		func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) })); err != nil || id == "" {
		t.Fatalf("create task: %q %v", id, err)
	}

	var title, search string
	if err := chromedp.Run(ctx,
		chromedp.Navigate(url+"/?task="+id),
		chromedp.Poll(`document.getElementById('detail-title') !== null`, nil),
		chromedp.Evaluate(`document.getElementById('detail-title').innerText`, &title),
		chromedp.Evaluate(`location.search`, &search),
	); err != nil {
		t.Fatal(err)
	}
	if title != "deep link me" {
		t.Errorf("detail title = %q, want the linked task", title)
	}
	if !strings.Contains(search, "task="+id) {
		t.Errorf("location.search = %q, want the task id in it", search)
	}

	// The copy button reports back that it copied.
	var btn string
	if err := chromedp.Run(ctx,
		chromedp.Click("#copy-link-btn", chromedp.ByID),
		chromedp.Poll(`document.getElementById('copy-link-btn').textContent.includes('copied')`, nil),
		chromedp.Evaluate(`document.getElementById('copy-link-btn').textContent.trim()`, &btn),
	); err != nil {
		t.Fatalf("copy link: %v", err)
	}
	if btn != "Link copied" {
		t.Errorf("copy button = %q, want Link copied", btn)
	}

	// Going back to the list drops the task from the URL.
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`goBackToList()`, nil),
		chromedp.Evaluate(`location.search`, &search),
	); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(search, "task=") {
		t.Errorf("location.search = %q after going back, want no task", search)
	}
}
