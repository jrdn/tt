package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrdn/tt/internal/client"
)

// Account commands pick their server from --server, TT_SERVER, the repo's
// .tt.json, then the default set by tt login, in that order.
func TestServerFor_Precedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("TT_SERVER", "")
	t.Setenv("TT_PROJECT", "")
	t.Chdir(t.TempDir())

	if _, err := serverFor(""); err == nil || !strings.Contains(err.Error(), "tt login --server") {
		t.Errorf("no server anywhere: err = %v", err)
	}

	if err := client.SetDefaultServer("http://default.example/"); err != nil {
		t.Fatal(err)
	}
	expect(t, "", "http://default.example")

	repo := t.TempDir()
	if _, err := client.WriteProject(repo, "http://repo.example", "demo"); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "sub")
	os.Mkdir(sub, 0755)
	t.Chdir(sub)
	expect(t, "", "http://repo.example")

	t.Setenv("TT_SERVER", "http://env.example")
	expect(t, "", "http://env.example")
	expect(t, "http://flag.example", "http://flag.example")
}

func expect(t *testing.T, flag, want string) {
	t.Helper()
	got, err := serverFor(flag)
	if err != nil || got != want {
		t.Errorf("serverFor(%q) = %q, %v; want %q", flag, got, err, want)
	}
}

// TestProjectMemberLists checks `tt project member <slug>` renders the roster
// from the server instead of requiring handle+role.
func TestProjectMemberLists(t *testing.T) {
	var gotMethod, gotPath, gotAuth, tokenKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/token" {
			tokenKey = r.Header.Get("Authorization")
			io.WriteString(w, `{"token":"jwt-test"}`)
			return
		}
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		io.WriteString(w, `{"project":"tt","members":[`+
			`{"handle":"jrdn","kind":"user","role":"owner"},`+
			`{"handle":"hermes","kind":"agent","role":"member"}]}`)
	}))
	defer srv.Close()
	t.Setenv("TT_SERVER", srv.URL)
	t.Setenv("TT_API_KEY", "test-key")

	server := ""
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	cmd := projectMemberCmd(&server)
	cmd.SetArgs([]string{"tt"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	runErr := cmd.Execute()
	os.Stdout = old
	w.Close()
	out, _ := io.ReadAll(r)

	if runErr != nil {
		t.Fatalf("member list: %v", runErr)
	}
	if gotMethod != "GET" || gotPath != "/api/v1/projects/tt/members" {
		t.Errorf("request = %s %s, want GET /api/v1/projects/tt/members", gotMethod, gotPath)
	}
	if !strings.Contains(gotAuth, "jwt-test") {
		t.Errorf("Authorization = %q, want the exchanged JWT", gotAuth)
	}
	if !strings.Contains(tokenKey, "test-key") {
		t.Errorf("token exchange Authorization = %q, want the API key", tokenKey)
	}
	rows := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(rows) != 3 || !strings.Contains(rows[0], "HANDLE") {
		t.Fatalf("output = %q, want a header plus two rows", string(out))
	}
	for _, want := range []string{"jrdn", "owner", "hermes", "member"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output %q missing %q", string(out), want)
		}
	}
}

// TestProjectMemberNeedsRole checks a handle without a role is rejected
// rather than silently listing.
func TestProjectMemberNeedsRole(t *testing.T) {
	server := ""
	cmd := projectMemberCmd(&server)
	cmd.SetArgs([]string{"tt", "alice"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "give a role") {
		t.Errorf("err = %v, want a role prompt", err)
	}
}
