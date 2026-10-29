package api_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jrdn/tt/internal/api"
)

// TestIndexShowsVersion checks the header carries the build version in both
// modes (tt web and tt server) and that the placeholder never leaks through.
func TestIndexShowsVersion(t *testing.T) {
	old := api.Version
	t.Cleanup(func() { api.Version = old })
	api.Version = "v1.2.3"

	handlers := map[string]func() string{
		"local":  func() string { return serveIndex(t, false) },
		"server": func() string { return serveIndex(t, true) },
	}
	for name, body := range handlers {
		got := body()
		want := `<span class="version" id="tt-version" title="tt build version">v1.2.3</span>`
		if !strings.Contains(got, want) {
			t.Errorf("%s: header missing %q", name, want)
		}
		if strings.Contains(got, "{{TT_VERSION}}") {
			t.Errorf("%s: version placeholder left unreplaced", name)
		}
		if name == "server" && !strings.Contains(got, "const TT_SERVER = true;") {
			t.Error("server: TT_CONFIG was not turned on")
		}
		if name == "local" && !strings.Contains(got, "const TT_SERVER = /*TT_CONFIG*/null;") {
			t.Error("local: TT_CONFIG should stay null")
		}
	}
}

// TestIndexEscapesVersion keeps a version string from injecting markup into
// the page.
func TestIndexEscapesVersion(t *testing.T) {
	old := api.Version
	t.Cleanup(func() { api.Version = old })
	api.Version = `1.0"><script>alert(1)</script>`

	if got := serveIndex(t, false); strings.Contains(got, "<script>alert(1)</script>") {
		t.Fatal("version was not escaped")
	}
}

func serveIndex(t *testing.T, server bool) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	if server {
		api.ServerIndex(rec, req)
	} else {
		api.Index(rec, req)
	}
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}
