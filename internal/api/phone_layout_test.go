//go:build ui

package api_test

import (
	"encoding/json"
	"testing"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// phoneWidths are the CSS widths of the two common phone classes: a small
// Android handset and an iPhone at default zoom.
var phoneWidths = []int64{360, 390}

// TestPhoneLayoutHasNoHorizontalScroll covers the header and list views at
// phone widths: the five header controls used to add up to more than the
// viewport, which scrolled the whole page sideways.
func TestPhoneLayoutHasNoHorizontalScroll(t *testing.T) {
	ctx, url := newBrowser(t)
	for _, w := range phoneWidths {
		var m []float64
		if err := chromedp.Run(ctx,
			emulation.SetDeviceMetricsOverride(w, 844, 3, true),
			chromedp.Navigate(url),
			chromedp.WaitVisible("#task-list"),
			chromedp.Evaluate(`fetch('/api/tasks', {method:'POST', body: JSON.stringify({title:'phone task'})}).then(r => r.ok)`, nil,
				func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }),
			chromedp.Evaluate(`loadTasks()`, nil),
			chromedp.Poll(`document.querySelector('.task-item') !== null`, nil),
			chromedp.Evaluate(`(() => {
				const h = document.querySelector('header').getBoundingClientRect();
				const item = document.querySelector('.task-item').getBoundingClientRect();
				const last = document.querySelector('#theme-btn').getBoundingClientRect();
				return [document.documentElement.scrollWidth, document.documentElement.clientWidth,
					h.right, item.height, last.right, window.innerWidth];
			})()`, &m),
		); err != nil {
			t.Fatalf("width %d: %v", w, err)
		}
		if len(m) != 6 {
			t.Fatalf("width %d: got %d numbers, want 6", w, len(m))
		}
		scrollWidth, clientWidth, headerRight, itemHeight, lastRight, innerWidth := m[0], m[1], m[2], m[3], m[4], m[5]
		if scrollWidth > clientWidth+1 {
			t.Errorf("width %d: page scrolls sideways (scrollWidth %.0f > clientWidth %.0f)", w, scrollWidth, clientWidth)
		}
		if headerRight > innerWidth+1 {
			t.Errorf("width %d: header ends at %.0f, past the viewport %.0f", w, headerRight, innerWidth)
		}
		if lastRight > innerWidth+1 {
			t.Errorf("width %d: rightmost header control ends at %.0f, past the viewport %.0f", w, lastRight, innerWidth)
		}
		if itemHeight < 44 {
			t.Errorf("width %d: task row is %.0fpx tall, want at least 44 for a thumb", w, itemHeight)
		}
	}
}

// TestNewTaskSheetFitsAPhone covers the reported symptom: the new task dialog
// did not fit a phone at default zoom. It is now a full-screen sheet whose
// fields are 16px (so mobile browsers do not zoom into them) and whose
// actions sit at the bottom of the screen.
func TestNewTaskSheetFitsAPhone(t *testing.T) {
	ctx, url := newBrowser(t)
	for _, w := range phoneWidths {
		var raw string
		if err := chromedp.Run(ctx,
			emulation.SetDeviceMetricsOverride(w, 844, 3, true),
			chromedp.Navigate(url),
			chromedp.WaitVisible("#task-list"),
			chromedp.Evaluate(`openNewTask()`, nil),
			chromedp.Poll(`document.querySelector('#new-task-overlay.open') !== null`, nil),
			chromedp.Evaluate(`(() => {
				const box = el => { const r = el.getBoundingClientRect(); return [r.left, r.top, r.width, r.height, r.bottom]; };
				return JSON.stringify({
					modal:  box(document.querySelector('.modal')),
					footer: box(document.querySelector('.modal-footer')),
					btns:   [...document.querySelectorAll('.modal-footer button')].map(b => b.getBoundingClientRect().height),
					font:   getComputedStyle(document.getElementById('nt-title')).fontSize,
					vw:     window.innerWidth,
					vh:     window.innerHeight,
					scroll: document.documentElement.scrollWidth,
				});
			})()`, &raw),
		); err != nil {
			t.Fatalf("width %d: %v", w, err)
		}
		var m struct {
			Modal  []float64 `json:"modal"`
			Footer []float64 `json:"footer"`
			Btns   []float64 `json:"btns"`
			Font   string    `json:"font"`
			Vw     float64   `json:"vw"`
			Vh     float64   `json:"vh"`
			Scroll float64   `json:"scroll"`
		}
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("width %d: %v", w, err)
		}
		if m.Modal[2] < m.Vw-1 || m.Modal[3] < m.Vh-1 {
			t.Errorf("width %d: dialog is %.0fx%.0f, want the full %.0fx%.0f screen", w, m.Modal[2], m.Modal[3], m.Vw, m.Vh)
		}
		if m.Modal[0] != 0 || m.Modal[1] != 0 {
			t.Errorf("width %d: dialog starts at %.0f,%.0f, want 0,0", w, m.Modal[0], m.Modal[1])
		}
		if bottom := m.Footer[4]; bottom < m.Vh-1 || bottom > m.Vh+1 {
			t.Errorf("width %d: actions end at %.0f, want them at the bottom of the screen (%.0f)", w, bottom, m.Vh)
		}
		for i, h := range m.Btns {
			if h < 43.5 {
				t.Errorf("width %d: action button %d is %.0fpx tall, want at least 44", w, i, h)
			}
		}
		if m.Font != "16px" {
			t.Errorf("width %d: field font-size = %s, want 16px so mobile browsers do not zoom on focus", w, m.Font)
		}
		if m.Scroll > m.Vw+1 {
			t.Errorf("width %d: page scrolls sideways with the dialog open (%.0f > %.0f)", w, m.Scroll, m.Vw)
		}
		if err := chromedp.Run(ctx, chromedp.Evaluate(`closeNewTask()`, nil)); err != nil {
			t.Fatal(err)
		}
	}
}

// TestPhoneDetailViewFits checks the detail view keeps its single-panel
// layout with a back button and no sideways scroll at phone widths.
func TestPhoneDetailViewFits(t *testing.T) {
	ctx, url := newBrowser(t)
	var m []float64
	if err := chromedp.Run(ctx,
		emulation.SetDeviceMetricsOverride(390, 844, 3, true),
		chromedp.Navigate(url),
		chromedp.WaitVisible("#task-list"),
		chromedp.Evaluate(`fetch('/api/tasks', {method:'POST', body: JSON.stringify({title:'detail task', description:'**bold** and https://example.com/a/very/long/path/that/keeps/going/on/and/on'})}).then(r => r.ok)`, nil,
			func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }),
		chromedp.Evaluate(`loadTasks()`, nil),
		chromedp.Poll(`document.querySelector('.task-item') !== null`, nil),
		chromedp.Evaluate(`document.querySelector('.task-item').click()`, nil),
		chromedp.Poll(`document.querySelector('#status-sel') !== null`, nil),
		chromedp.Evaluate(`(() => {
			const back = document.getElementById('back-btn');
			return [document.documentElement.scrollWidth, document.documentElement.clientWidth,
				getComputedStyle(back).display === 'none' ? 0 : 1,
				getComputedStyle(document.querySelector('aside')).display === 'none' ? 0 : 1,
				document.querySelector('.detail-header').getBoundingClientRect().right];
		})()`, &m),
	); err != nil {
		t.Fatal(err)
	}
	if len(m) != 5 {
		t.Fatalf("got %d numbers, want 5", len(m))
	}
	if m[0] > m[1]+1 {
		t.Errorf("detail view scrolls sideways (%.0f > %.0f)", m[0], m[1])
	}
	if m[2] != 1 {
		t.Error("back button is hidden in the detail view on a phone")
	}
	if m[3] != 0 {
		t.Error("task list is still shown next to the detail view on a phone")
	}
	if m[4] > 391 {
		t.Errorf("detail header ends at %.0f, past the viewport", m[4])
	}
}
