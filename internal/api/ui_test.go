//go:build ui

package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
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
