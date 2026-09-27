//go:build e2e

// Package e2e drives the real tt binary. The parity test runs one scenario
// against a local SQLite database and against a tt server project, and
// requires identical output. Run with: mise run pg:up && mise run test:e2e
package e2e

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

var ttBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tt-e2e-bin")
	if err != nil {
		panic(err)
	}
	ttBin = filepath.Join(dir, "tt")
	args := []string{"build", "-o", ttBin}
	if os.Getenv("TT_E2E_COVERDIR") != "" {
		args = append(args, "-cover", "-coverpkg=github.com/jrdn/tt/...")
	}
	if out, err := exec.Command("go", append(args, "github.com/jrdn/tt/cmd/tt")...).CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build tt: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// env is an isolated place to run tt: its own home and config directory,
// working directory and environment. Nothing is inherited from the
// developer's environment except PATH.
type env struct {
	t    *testing.T
	dir  string
	vars map[string]string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	home := t.TempDir()
	editor := filepath.Join(home, "editor.sh")
	// A scripted $EDITOR for `tt edit`: retitle, reprioritize, clear the assignee.
	script := "#!/bin/sh\nsed -i.bak -e 's/^# .*/# Edited in editor/' -e 's/^priority: .*/priority: 1/' -e 's/^assignee:.*/assignee:/' \"$1\"\n"
	if err := os.WriteFile(editor, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "work")
	os.MkdirAll(dir, 0755)
	e := &env{t: t, dir: dir, vars: map[string]string{
		"PATH":            os.Getenv("PATH"),
		"HOME":            home,
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"TT_KEYRING":      "off", // never touch the developer's keychain
		"EDITOR":          editor,
		"NO_COLOR":        "1",
	}}
	if d := os.Getenv("TT_E2E_COVERDIR"); d != "" {
		e.vars["GOCOVERDIR"] = d
	}
	return e
}

func (e *env) with(kv ...string) *env {
	c := &env{t: e.t, dir: e.dir, vars: map[string]string{}}
	for k, v := range e.vars {
		c.vars[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		c.vars[kv[i]] = kv[i+1]
	}
	return c
}

func (e *env) environ() []string {
	var out []string
	for k, v := range e.vars {
		out = append(out, k+"="+v)
	}
	return out
}

// run executes tt and returns combined output and the exit code.
func (e *env) run(args ...string) (string, int) {
	e.t.Helper()
	cmd := exec.Command(ttBin, args...)
	cmd.Dir = e.dir
	cmd.Env = e.environ()
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		return string(out), ee.ExitCode()
	case err != nil:
		e.t.Fatalf("run tt %v: %v", args, err)
	}
	return string(out), 0
}

// must runs tt and fails the test on a non-zero exit.
func (e *env) must(args ...string) string {
	e.t.Helper()
	out, code := e.run(args...)
	if code != 0 {
		e.t.Fatalf("tt %s: exit %d\n%s", strings.Join(args, " "), code, out)
	}
	return out
}

// server is a running tt server on a fresh Postgres database.
type server struct {
	url   string
	key   string // API key for jrdn, a server admin
	admin *env   // env with TT_DATABASE_URL etc., for `tt server` subcommands
}

func startServer(t *testing.T) *server {
	t.Helper()
	base := os.Getenv("TT_TEST_POSTGRES_URL")
	if base == "" {
		t.Skip("set TT_TEST_POSTGRES_URL to run (mise run pg:up)")
	}
	pg, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	name := "tt_e2e_" + randHex(6)
	if _, err := pg.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pg.Exec("DROP DATABASE " + name + " WITH (FORCE)")
		pg.Close()
	})

	master := make([]byte, 32)
	rand.Read(master)
	port := freePort(t)
	url := "http://127.0.0.1:" + port
	admin := newEnv(t).with(
		"TT_DATABASE_URL", strings.Replace(base, "/tt_test", "/"+name, 1),
		"TT_MASTER_KEY", base64.StdEncoding.EncodeToString(master),
		"TT_BASE_URL", url,
	)
	admin.must("server", "add-user", "jrdn", "--admin")
	key := strings.TrimSpace(admin.must("server", "create-key", "jrdn"))

	var logs bytes.Buffer
	cmd := exec.Command(ttBin, "server", "--addr", "127.0.0.1:"+port)
	cmd.Env, cmd.Stdout, cmd.Stderr = admin.environ(), &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Signal(os.Interrupt)
		cmd.Wait()
		if t.Failed() {
			t.Logf("tt server log:\n%s", logs.String())
		}
	})
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if resp, err := http.Get(url + "/.well-known/jwks.json"); err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("tt server didn't start:\n%s", logs.String())
		}
	}
	return &server{url: url, key: key, admin: admin}
}

func freePort(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// remoteEnv returns an env in a git repo pointed at a server project, with
// jrdn logged in.
func remoteEnv(t *testing.T, s *server, project string) *env {
	e := newEnv(t)
	gitInit(t, e.dir)
	e.must("init", "--server", s.url, "--project", project)
	e.must("login", "--key", s.key)
	return e
}

func gitInit(t *testing.T, dir string) {
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

// normalizer replaces run-specific values (task IDs, comment IDs,
// timestamps) with stable placeholders so transcripts can be compared.
type normalizer struct{ ids map[string]string }

var (
	tsRe        = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z`)
	commentIDRe = regexp.MustCompile(`("id": "|comment |\[)[0-9a-z]{7}("|\]| added)`)
	dayRe       = regexp.MustCompile(`\[\d{4}-\d{2}-\d{2}\]`)
	exportedRe  = regexp.MustCompile(`"exported_at": "[^"]*"`)
)

func (n *normalizer) apply(s string) string {
	// Longest first, so a full ID is replaced before any prefix of it.
	ids := make([]string, 0, len(n.ids))
	for id := range n.ids {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return len(ids[i]) > len(ids[j]) })
	for _, id := range ids {
		s = strings.ReplaceAll(s, id, n.ids[id])
	}
	s = tsRe.ReplaceAllString(s, "<TS>")
	s = commentIDRe.ReplaceAllString(s, "${1}<CID>${2}")
	s = dayRe.ReplaceAllString(s, "[<DAY>]")
	return exportedRe.ReplaceAllString(s, `"exported_at": "<TS>"`)
}

// scenario runs the same tt session in e and returns its normalized
// transcript. Content checks guard against both modes being wrong the same
// way.
func scenario(t *testing.T, e *env, mode string) string {
	n := &normalizer{ids: map[string]string{}}
	var out strings.Builder
	step := func(args ...string) string {
		t.Helper()
		o, code := e.run(args...)
		fmt.Fprintf(&out, "$ tt %s  [exit %d]\n%s\n", n.apply(strings.Join(args, " ")), code, n.apply(o))
		return o
	}
	add := func(name string, args ...string) string {
		t.Helper()
		o := e.must(append(append([]string{"add"}, args...), "--created-by", "jrdn", "--json")...)
		var tk struct{ ID string }
		if err := json.Unmarshal([]byte(o), &tk); err != nil || tk.ID == "" {
			t.Fatalf("[%s] add %v: %v\n%s", mode, args, err, o)
		}
		n.ids[tk.ID] = name
		// Prefixes are passed on purpose (prefix lookup); name them too.
		n.ids[tk.ID[:4]] = name[:len(name)-1] + ":4>"
		n.ids[tk.ID[:5]] = name[:len(name)-1] + ":5>"
		fmt.Fprintf(&out, "$ tt add %s\n%s\n", n.apply(strings.Join(args, " ")), n.apply(o))
		return tk.ID
	}
	check := func(cond bool, format string, args ...any) {
		t.Helper()
		if !cond {
			t.Errorf("[%s] "+format, append([]any{mode}, args...)...)
		}
	}

	a := add("<A>", "Alpha task", "-p", "1")
	b := add("<B>", "Beta child", "--parent", a, "--assignee", "bot", "--due", "2026-12-01", "-d", "Some **markdown** body")
	c := add("<C>", "Gamma blocker")
	d := add("<D>", "Delta")

	step("relate", c, "blocks", b)
	step("comment", b, "hello there", "--author", "jrdn")
	step("comment", b, "second note", "--author", "jrdn")
	check(strings.Contains(step("ls"), "Beta child"), "ls is missing a task")
	step("ls", "--ready")
	step("ls", "--all", "--json")
	step("ls", "--assignee", "bot")
	step("ls", "--parent", a[:4])
	show := step("show", b)
	check(strings.Contains(show, "hello there") && strings.Contains(show, "blocked by"), "show lacks comment or relation:\n%s", show)
	step("show", b, "--json")
	step("show", b[:5])
	check(strings.Contains(step("search", "beta"), "Beta child"), "search missed Beta child")
	step("search", "hlo thr") // fuzzy match on a comment
	step("update", a, "--status", "in_review")
	step("update", b, "--title", "Beta child renamed", "--priority", "0", "--due", "2027-01-15")
	check(strings.Contains(step("next", "--as", "bot"), "no ready tasks"), "next handed out a blocked task")
	step("done", c)
	next := step("next", "--as", "bot", "--json")
	check(strings.Contains(next, `"id": "`+b+`"`), "next after unblock didn't return B:\n%s", next)
	step("assign", "alice", d)
	step("claim", d, "--as", "carol")
	step("cancel", a)
	step("reopen", a)
	step("unrelate", c, "blocks", b)
	step("edit", d)
	step("show", d)
	step("graph")
	step("prime")
	step("ls", "--status", "in_progress")
	exp := step("export")
	check(strings.Count(exp, `"title":`) == 4, "export should hold 4 tasks:\n%s", exp)

	// Errors must match too.
	_, code := e.run("show", "zzzzzzz")
	check(code != 0, "show of a missing task succeeded")
	step("show", "zzzzzzz")
	step("update", b, "--status", "GARBAGE")
	step("add", "Bad priority", "-p", "9")
	step("relate", a, "sideways", b)
	return out.String()
}

// TestParity runs the scenario locally and against a server project, then
// imports the local database into a second project.
func TestParity(t *testing.T) {
	s := startServer(t)

	local := newEnv(t)
	gitInit(t, local.dir)
	localDB := filepath.Join(local.dir, "local.db")
	local = local.with("TASKS_DB", localDB)
	localOut := scenario(t, local, "local")

	remote := remoteEnv(t, s, "demo")
	remote.must("project", "create", "demo")
	remoteOut := scenario(t, remote, "server")

	if localOut != remoteOut {
		t.Errorf("local and server transcripts differ:\n%s", diff(t, localOut, remoteOut))
	}

	// Import keeps IDs, timestamps and authors, so exports match exactly.
	remote.must("project", "create", "imported")
	s.admin.must("server", "import", "--project", "imported", "--as", "jrdn", localDB)
	n := &normalizer{}
	want := n.apply(local.must("export"))
	got := n.apply(remote.with("TT_PROJECT", "imported").must("export"))
	if got != want {
		t.Errorf("imported project's export differs from the local export:\n%s", diff(t, want, got))
	}
	if t.Failed() {
		t.Logf("local transcript:\n%s", localOut)
	}
}

// TestServerAccounts covers the account commands end to end: agents, keys,
// offline attenuation, revocation, logout, and local-only commands.
func TestServerAccounts(t *testing.T) {
	s := startServer(t)
	e := remoteEnv(t, s, "demo")
	e.must("project", "create", "demo")

	if who := e.must("whoami"); !strings.Contains(who, "jrdn (user), server admin") || !strings.Contains(who, "demo") {
		t.Errorf("whoami = %q", who)
	}
	if ls := e.must("project", "ls"); !strings.Contains(ls, "demo") || !strings.Contains(ls, "owner") {
		t.Errorf("project ls = %q", ls)
	}

	e.must("agent", "add", "claude/opus4.7")
	agentKey := strings.TrimSpace(e.must("key", "create", "--agent", "claude/opus4.7", "--name", "e2e"))
	agent := e.with("TT_API_KEY", agentKey)
	if who := agent.must("whoami"); !strings.Contains(who, "on behalf of jrdn") || !strings.Contains(who, "member") {
		t.Errorf("agent whoami = %q (agent keys should default to member)", who)
	}
	out := agent.must("add", "Agent task", "--created-by", "someone-else", "--json")
	var tk struct {
		ID        string
		CreatedBy string `json:"created_by"`
	}
	json.Unmarshal([]byte(out), &tk)
	if tk.CreatedBy != "claude/opus4.7" {
		t.Errorf("created_by = %q, want the agent's handle from its token", tk.CreatedBy)
	}
	if _, code := agent.run("project", "member", "demo", "jrdn", "viewer"); code == 0 {
		t.Error("a member-role agent changed project membership")
	}

	// A read-only sub-agent key, derived offline.
	sub := strings.TrimSpace(agent.must("key", "attenuate", "--actions", "task:read", "--label", "reader"))
	reader := e.with("TT_API_KEY", sub)
	if ls := reader.must("ls"); !strings.Contains(ls, "Agent task") {
		t.Errorf("reader ls = %q", ls)
	}
	if o, code := reader.run("add", "Nope"); code == 0 || !strings.Contains(o, "task:create") {
		t.Errorf("read-only key created a task (exit %d): %s", code, o)
	}
	if o, code := reader.run("comment", tk.ID, "nope"); code == 0 {
		t.Errorf("read-only key commented: %s", o)
	}

	// Revoking the agent's key also kills keys derived from it.
	keys := e.must("key", "ls", "--json")
	var list []struct{ ID, Handle string }
	json.Unmarshal([]byte(keys), &list)
	var agentKeyID string
	for _, k := range list {
		if k.Handle == "claude/opus4.7" {
			agentKeyID = k.ID
		}
	}
	if agentKeyID == "" {
		t.Fatalf("agent key not listed:\n%s", keys)
	}
	e.must("key", "revoke", agentKeyID)
	for name, x := range map[string]*env{"agent": agent, "sub-agent": reader} {
		if o, code := x.run("ls"); code == 0 || !strings.Contains(o, "tt login") {
			t.Errorf("%s key still works after revocation (exit %d): %s", name, code, o)
		}
	}

	if o, code := e.run("sync", "push"); code == 0 || !strings.Contains(o, "works on local databases") {
		t.Errorf("tt sync ran in server mode (exit %d): %s", code, o)
	}

	e.must("logout")
	if o, code := e.run("ls"); code == 0 || !strings.Contains(o, "not logged in") {
		t.Errorf("ls after logout (exit %d): %s", code, o)
	}
	// Logout revoked the key on the server, not just locally.
	if o, code := e.with("TT_API_KEY", s.key).run("ls"); code == 0 {
		t.Errorf("key still works after logout: %s", o)
	}
}

// diff shows a unified diff of two transcripts.
func diff(t *testing.T, a, b string) string {
	dir := t.TempDir()
	fa, fb := filepath.Join(dir, "local"), filepath.Join(dir, "server")
	os.WriteFile(fa, []byte(a), 0644)
	os.WriteFile(fb, []byte(b), 0644)
	out, _ := exec.Command("diff", "-u", fa, fb).CombinedOutput()
	return string(out)
}
