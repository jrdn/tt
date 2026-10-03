//go:build e2e

// Package e2e drives the real tt binary. The parity test runs one scenario
// against a local SQLite database and against a tt server project, and
// requires identical output. Run with: mise run pg:up && mise run test:e2e
package e2e

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
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
	// A scripted $EDITOR: applies the sed script in $HOME/edit.sed, set per
	// edit with setEditor. The default retitles, reprioritizes and clears
	// the assignee.
	script := "#!/bin/sh\nsed -i.bak -f \"$HOME/edit.sed\" \"$1\"\n"
	if err := os.WriteFile(editor, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "edit.sed"),
		[]byte("s/^# .*/# Edited in editor/\ns/^priority: .*/priority: 1/\ns/^assignee:.*/assignee:/\n"), 0644); err != nil {
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

// setEditor makes the next `tt edit` / `tt add` (no title) apply these sed
// commands to the task file.
func (e *env) setEditor(cmds ...string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.vars["HOME"], "edit.sed"), []byte(strings.Join(cmds, "\n")+"\n"), 0644); err != nil {
		e.t.Fatal(err)
	}
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
	db    *sql.DB
	jrdn  string // jrdn's principal ID
}

// session logs jrdn in as a browser would after GitHub sign-in, returning
// the session cookie value.
func (s *server) session(t *testing.T) string {
	t.Helper()
	token := randHex(32)
	sum := sha256.Sum256([]byte(token))
	if _, err := s.db.Exec(`INSERT INTO sessions (token_hash, principal_id, expires_at) VALUES ($1, $2, now() + interval '1 hour')`,
		sum[:], s.jrdn); err != nil {
		t.Fatal(err)
	}
	return token
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
	added := admin.must("server", "add-user", "jrdn", "--admin")
	jrdn := regexp.MustCompile(`prn_[0-9a-z]+`).FindString(added)
	key := strings.TrimSpace(admin.must("server", "create-key", "jrdn"))
	db, err := sql.Open("pgx", admin.vars["TT_DATABASE_URL"])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

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
	return &server{url: url, key: key, admin: admin, db: db, jrdn: jrdn}
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

// scenarioMore covers the flags and forms scenario doesn't: every update
// field (set and clear), several IDs at once, every relation type, --json
// on write commands, full edits, and creating a task from the editor.
func scenarioMore(t *testing.T, e *env, mode string) string {
	n := &normalizer{ids: map[string]string{}}
	var out strings.Builder
	step := func(args ...string) string {
		t.Helper()
		o, code := e.run(args...)
		fmt.Fprintf(&out, "$ tt %s  [exit %d]\n%s\n", n.apply(strings.Join(args, " ")), code, n.apply(o))
		return o
	}
	track := func(name, o string) string {
		t.Helper()
		var tk struct{ ID string }
		if err := json.Unmarshal([]byte(o), &tk); err != nil || tk.ID == "" {
			t.Fatalf("[%s] no task in %q: %v", mode, o, err)
		}
		n.ids[tk.ID] = name
		n.ids[tk.ID[:4]] = name[:len(name)-1] + ":4>"
		fmt.Fprintf(&out, "(created %s)\n%s\n", name, n.apply(o))
		return tk.ID
	}
	check := func(cond bool, format string, args ...any) {
		t.Helper()
		if !cond {
			t.Errorf("[%s] "+format, append([]any{mode}, args...)...)
		}
	}
	field := func(id, name string) any {
		t.Helper()
		var d struct{ Task map[string]any }
		json.Unmarshal([]byte(e.must("show", id, "--json")), &d)
		return d.Task[name]
	}

	p := track("<P>", e.must("add", "Parent", "--created-by", "jrdn", "--json"))
	q := track("<Q>", e.must("add", "Other parent", "--created-by", "jrdn", "--json"))
	// Positional description form.
	x := track("<X>", e.must("add", "Xray", "positional description", "--created-by", "jrdn", "--json"))
	y := track("<Y>", e.must("add", "Yankee", "--parent", p, "--assignee", "bot", "--due", "2026-05-01",
		"-d", "desc", "--created-by", "jrdn", "--json"))
	z := track("<Z>", e.must("add", "Zulu", "-p", "3", "--created-by", "jrdn", "--json"))
	check(field(x, "description") == "positional description", "positional description not stored")

	// Every update field, then clearing the optional ones.
	step("update", y, "--description", "new body", "--assignee", "carol", "--parent", q, "--json")
	check(field(y, "parent_id") == q && field(y, "assignee") == "carol" && field(y, "description") == "new body",
		"update didn't set description/assignee/parent")
	step("update", y, "--assignee", "", "--description", "", "--due", "")
	check(field(y, "assignee") == nil && field(y, "due_date") == nil, "update with empty values didn't clear fields")
	step("update", y, "--parent", p)
	step("update", y, "--due", "not-a-date")
	step("update", y, "--parent", "zzzzzzz")
	step("update", y, "--priority", "7")

	// Several IDs at once, with JSON output.
	step("update", x, z, "--priority", "1", "--json")
	step("done", x, z, "--json")
	check(field(x, "status") == "done" && field(z, "status") == "done", "done with two IDs didn't close both")
	step("reopen", x, z, "--json")
	step("cancel", x, z)
	step("reopen", x, z)
	step("assign", "dana", x, z, "--json")
	check(field(z, "assignee") == "dana", "assign with two IDs missed one")
	step("done", x, "zzzzzzz") // one bad ID among good ones

	// Every relation type, with JSON output, and how show and graph render them.
	step("relate", x, "duplicates", z, "--json")
	step("relate", x, "related", y, "--json")
	step("relate", z, "blocks", y, "--json")
	step("relate", z, "blocks", y) // idempotent
	step("show", x)
	step("show", y)
	step("graph")
	step("unrelate", x, "duplicates", z)
	step("unrelate", x, "related", y)
	step("unrelate", z, "blocks", y)
	step("show", x, "--json")

	// External refs: add, update the URL, re-point to another task, link by prefix.
	step("link", x, "linear", "ENG-1", "https://linear.app/x/ENG-1", "--json")
	step("link", x[:4], "github", "42")
	step("link", x, "linear", "ENG-1", "https://linear.app/x/ENG-1-renamed")
	step("show", x)
	step("link", z, "github", "42", "--json")
	step("show", z, "--json")
	step("show", y, "--json") // no refs: an empty list
	step("link", "zzzzzzz", "linear", "ENG-9")

	// Comment JSON, and claim JSON.
	step("comment", x, "with **markdown**", "--json")
	step("claim", z, "--as", "erin", "--json")

	// A full edit: status, due date, parent, body; then clearing them.
	e.setEditor("s/^status: .*/status: in_review/", "s/^due:.*/due: 2027-02-03/", "s/^parent:.*/parent: "+q+"/",
		"$a\\", "Body added in the editor.")
	step("edit", x)
	check(field(x, "status") == "in_review" && field(x, "parent_id") == q && field(x, "due_date") == "2027-02-03",
		"edit didn't apply status/due/parent")
	e.setEditor("s/^due:.*/due:/", "s/^parent:.*/parent:/", "s/^status: .*/status: done/")
	step("edit", x)
	check(field(x, "parent_id") == nil && field(x, "closed_at") != nil, "edit didn't clear parent / set closed_at")
	step("show", x)
	e.setEditor("s/^status: .*/status: GARBAGE/")
	step("edit", x)
	check(field(x, "status") == "done", "edit saved an invalid status")

	// Creating a task from the editor (no title).
	e.setEditor("s/^# .*/# Made in the editor/", "s/^priority: .*/priority: 0/", "s/^assignee:.*/assignee: frank/")
	track("<E>", e.must("add", "--created-by", "jrdn", "--json"))
	ls := e.must("ls", "--json")
	check(strings.Contains(ls, `"title": "Made in the editor"`) && strings.Contains(ls, `"assignee": "frank"`),
		"task from the editor missing:\n%s", ls)

	step("ls", "--all", "--json")
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

	t.Run("more", func(t *testing.T) {
		l := newEnv(t)
		gitInit(t, l.dir)
		l = l.with("TASKS_DB", filepath.Join(l.dir, "more.db"))
		r := remoteEnv(t, s, "more")
		r.must("project", "create", "more")
		lo, ro := scenarioMore(t, l, "local"), scenarioMore(t, r, "server")
		if lo != ro {
			t.Errorf("local and server transcripts differ:\n%s", diff(t, lo, ro))
		}
		if t.Failed() {
			t.Logf("local transcript:\n%s", lo)
		}
	})

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

// background starts tt and returns a channel of its output lines and one
// that yields the exit code.
func (e *env) background(args ...string) (<-chan string, <-chan int) {
	e.t.Helper()
	cmd := exec.Command(ttBin, args...)
	cmd.Dir, cmd.Env = e.dir, e.environ()
	pr, pw, _ := os.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		e.t.Fatal(err)
	}
	pw.Close()
	lines, exit := make(chan string, 64), make(chan int, 1)
	go func() {
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	go func() {
		err := cmd.Wait()
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		exit <- code
	}()
	e.t.Cleanup(func() { cmd.Process.Kill() })
	return lines, exit
}

// waitLine returns the first output line matching re.
func waitLine(t *testing.T, lines <-chan string, re *regexp.Regexp) string {
	t.Helper()
	timeout := time.After(15 * time.Second)
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("tt exited before printing %s", re)
			}
			if m := re.FindString(l); m != "" {
				return m
			}
		case <-timeout:
			t.Fatalf("timed out waiting for %s", re)
		}
	}
}

func waitExit(t *testing.T, exit <-chan int, lines <-chan string) {
	t.Helper()
	select {
	case code := <-exit:
		var rest []string
		for l := range lines {
			rest = append(rest, l)
		}
		if code != 0 {
			t.Fatalf("tt login exited %d:\n%s", code, strings.Join(rest, "\n"))
		}
	case <-time.After(15 * time.Second):
		t.Fatal("tt login didn't finish")
	}
}

// browserPost submits a form as jrdn's logged-in browser, following
// redirects (for the loopback flow, to the CLI's own listener).
func browserPost(t *testing.T, s *server, session, path string, form url.Values) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", s.url+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "tt_session", Value: session})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestLogin drives `tt login` (loopback, the default) and `tt login
// --device` with the real CLI, playing the browser's part.
func TestLogin(t *testing.T) {
	s := startServer(t)
	session := s.session(t)

	t.Run("loopback", func(t *testing.T) {
		e := newEnv(t)
		gitInit(t, e.dir)
		e.must("init", "--server", s.url, "--project", "demo")
		lines, exit := e.background("login", "--no-browser")
		authURL := waitLine(t, lines, regexp.MustCompile(`http://\S+/auth/cli\?\S+`))

		// The approval page, as the browser shows it.
		req, _ := http.NewRequest("GET", authURL, nil)
		req.AddCookie(&http.Cookie{Name: "tt_session", Value: session})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		page, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		form := url.Values{}
		for _, m := range regexp.MustCompile(`name="(\w+)" value="([^"]*)"`).FindAllStringSubmatch(string(page), -1) {
			form.Set(m[1], html.UnescapeString(m[2]))
		}
		if form.Get("redirect_uri") == "" {
			t.Fatalf("approval page has no form:\n%s", page)
		}
		// Approving redirects to the CLI's listener, which answers.
		code, body := browserPost(t, s, session, "/auth/cli/approve", form)
		if code != 200 || !strings.Contains(body, "Logged in to tt") {
			t.Fatalf("after approve: %d %s", code, body)
		}
		waitExit(t, exit, lines)
		if who := e.must("whoami"); !strings.Contains(who, "jrdn") {
			t.Errorf("whoami after login = %q", who)
		}
	})

	t.Run("device", func(t *testing.T) {
		e := newEnv(t)
		lines, exit := e.background("login", "--device", "--server", s.url)
		userCode := waitLine(t, lines, regexp.MustCompile(`^\s*[A-Z0-9]{4}-[A-Z0-9]{4}\s*$`))
		userCode = strings.TrimSpace(userCode)
		if code, page := browserPost(t, s, session, "/auth/device/confirm", url.Values{"user_code": {userCode}}); code != 200 ||
			!strings.Contains(page, "Approve") {
			t.Fatalf("confirm: %d %s", code, page)
		}
		if code, _ := browserPost(t, s, session, "/auth/device/approve", url.Values{"user_code": {userCode}}); code != 200 {
			t.Fatalf("approve: %d", code)
		}
		waitExit(t, exit, lines)
		if who := e.must("whoami", "--server", s.url); !strings.Contains(who, "jrdn") {
			t.Errorf("whoami after device login = %q", who)
		}
	})
}

// After one `tt login --server`, account commands outside a repo use that
// server by default; logging out clears it.
func TestDefaultServer(t *testing.T) {
	s := startServer(t)
	e := newEnv(t)
	e.must("login", "--server", s.url, "--key", s.key)
	if who := e.must("whoami"); !strings.Contains(who, "jrdn") || !strings.Contains(who, s.url) {
		t.Errorf("whoami with default server = %q", who)
	}
	e.must("project", "create", "demo")
	if ls := e.must("project", "ls"); !strings.Contains(ls, "demo") {
		t.Errorf("project ls = %q", ls)
	}
	// Task commands outside a repo stay local.
	if out := e.must("ls"); strings.Contains(out, "error") {
		t.Errorf("local ls = %q", out)
	}
	e.must("logout")
	if o, code := e.run("whoami"); code == 0 || !strings.Contains(o, "tt login --server") {
		t.Errorf("whoami after logout (exit %d): %s", code, o)
	}
}
