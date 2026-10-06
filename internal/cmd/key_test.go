package cmd

import (
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jrdn/tt/internal/auth"
)

var keyTestMaster = []byte("0123456789abcdef0123456789abcdef")

// runAttenuate runs `tt key attenuate` with args and returns what it printed.
func runAttenuate(t *testing.T, args ...string) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	cmd := newKeyAttenuateCmd()
	cmd.SetArgs(args)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	runErr := cmd.Execute()
	os.Stdout = old
	w.Close()
	out, _ := io.ReadAll(r)
	return strings.TrimSpace(string(out)), runErr
}

func TestKeyAttenuate_Flags(t *testing.T) {
	root, _ := auth.MintKey(keyTestMaster, "k1")
	t.Setenv("TT_API_KEY", "")

	before := time.Now()
	out, err := runAttenuate(t, "--key", root, "--project", "tt,web", "--role", "member",
		"--task", "ab12cde", "--task", "fg34hij", "--actions", "task:read,comment:create",
		"--expires", "2h", "--label", "sub-reviewer")
	if err != nil {
		t.Fatal(err)
	}
	id, r, err := auth.VerifyKey(keyTestMaster, out, time.Now())
	if err != nil {
		t.Fatalf("VerifyKey: %v", err)
	}
	if id != "k1" {
		t.Errorf("id = %q", id)
	}
	if !slices.Equal(r.Projects, []string{"tt", "web"}) || r.MaxRole != auth.RoleMember ||
		!slices.Equal(r.Tasks, []string{"ab12cde", "fg34hij"}) || r.Label != "sub-reviewer" ||
		!slices.Equal(r.Actions, []auth.Action{auth.ActTaskRead, auth.ActCommentCreate}) {
		t.Errorf("restrictions = %+v", r)
	}
	if r.Expires.Before(before.Add(2*time.Hour-time.Minute)) || r.Expires.After(time.Now().Add(2*time.Hour+time.Minute)) {
		t.Errorf("expires = %v, want about 2h from now", r.Expires)
	}
}

func TestKeyAttenuate_ParentFromEnv(t *testing.T) {
	root, _ := auth.MintKey(keyTestMaster, "k1")
	t.Setenv("TT_API_KEY", root)
	out, err := runAttenuate(t, "--label", "x")
	if err != nil {
		t.Fatal(err)
	}
	if _, r, err := auth.VerifyKey(keyTestMaster, out, time.Now()); err != nil || r.Label != "x" {
		t.Errorf("r = %+v, err = %v", r, err)
	}
}

// Without --project the key keeps every project; the flag only narrows when given.
func TestKeyAttenuate_ProjectOnlyWhenSet(t *testing.T) {
	root, _ := auth.MintKey(keyTestMaster, "k1")
	t.Setenv("TT_API_KEY", "")
	out, err := runAttenuate(t, "--key", root, "--label", "x")
	if err != nil {
		t.Fatal(err)
	}
	if _, r, _ := auth.VerifyKey(keyTestMaster, out, time.Now()); r.Projects != nil {
		t.Errorf("projects = %v, want no limit", r.Projects)
	}
}

func TestKeyAttenuate_Errors(t *testing.T) {
	root, _ := auth.MintKey(keyTestMaster, "k1")
	t.Setenv("TT_API_KEY", "")
	cases := map[string][]string{
		"no parent key":   {"--label", "x"},
		"not a key":       {"--key", "ghp_abc", "--label", "x"},
		"bad role":        {"--key", root, "--role", "admin"},
		"bad action":      {"--key", root, "--actions", "task:read,nuke"},
		"no restrictions": {"--key", root},
		"unparseable ttl": {"--key", root, "--expires", "soon"},
		"positional args": {"--key", root, "--label", "x", "extra"},
	}
	for name, args := range cases {
		if out, err := runAttenuate(t, args...); err == nil {
			t.Errorf("%s: expected an error, got key %q", name, out)
		}
	}
}
