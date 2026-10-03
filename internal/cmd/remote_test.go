package cmd

import (
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
