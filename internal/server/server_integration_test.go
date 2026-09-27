//go:build integration

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"

	"github.com/jrdn/tt/internal/auth"
	ttdb "github.com/jrdn/tt/internal/db"
	"github.com/jrdn/tt/internal/task"
)

var testMaster = []byte("0123456789abcdef0123456789abcdef")

// newTestServer starts a server on a fresh Postgres database.
func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	base := os.Getenv("TT_TEST_POSTGRES_URL")
	if base == "" {
		t.Skip("set TT_TEST_POSTGRES_URL to run")
	}
	admin, err := sqlx.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	name := "tt_srv_" + task.NewID()
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	url := strings.Replace(base, "/tt_test", "/"+name, 1)
	srv, err := New(context.Background(), Config{DatabaseURL: url, MasterKey: testMaster})
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		hs.Close()
		srv.Close()
		admin.Exec("DROP DATABASE " + name + " WITH (FORCE)")
		admin.Close()
	})
	return srv, hs
}

type client struct {
	t     *testing.T
	base  string
	token string
}

func (c *client) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, r)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// must asserts a status code and returns the body.
func (c *client) must(want int, method, path string, body any) map[string]any {
	c.t.Helper()
	code, out := c.do(method, path, body)
	if code != want {
		c.t.Fatalf("%s %s = %d, want %d: %v", method, path, code, want, out)
	}
	return out
}

func (c *client) as(token string) *client { return &client{t: c.t, base: c.base, token: token} }

func TestServer_EndToEnd(t *testing.T) {
	srv, hs := newTestServer(t)
	ctx := context.Background()
	dir := srv.Directory()
	anon := &client{t: t, base: hs.URL}

	alice, _ := dir.CreateUser(ctx, "alice", nil, true)
	bob, _ := dir.CreateUser(ctx, "bob", nil, false)
	_, aliceKey, _ := dir.CreateKey(ctx, alice, alice, "", nil, "", time.Time{})
	_, bobKey, _ := dir.CreateKey(ctx, bob, bob, "", nil, "", time.Time{})

	// Unauthenticated and bad credentials.
	anon.must(401, "GET", "/api/v1/whoami", nil)
	anon.as("tt_ak_garbage").must(401, "GET", "/api/v1/whoami", nil)
	anon.as("not.a.jwt").must(401, "GET", "/api/v1/whoami", nil)

	// Exchange a key for a JWT; whoami works with either.
	tok := anon.as(aliceKey).must(200, "POST", "/api/v1/token", nil)["token"].(string)
	a := anon.as(tok)
	if who := a.must(200, "GET", "/api/v1/whoami", nil); who["handle"] != "alice" || who["admin"] != true {
		t.Fatalf("whoami = %v", who)
	}

	// Raw keys work directly as bearer tokens (middleware exchanges them).
	aRaw := anon.as(aliceKey)
	aRaw.must(201, "POST", "/api/v1/projects", map[string]string{"slug": "tt", "name": "tt"})
	anon.as(bobKey).must(403, "POST", "/api/v1/projects", map[string]string{"slug": "bobs"})

	// Bob isn't a member: the project reads as not found.
	b := anon.as(bobKey)
	b.must(404, "GET", "/api/v1/projects/tt/tasks", nil)

	// Alice adds Bob as a viewer: read yes, write no.
	aRaw.must(200, "PUT", "/api/v1/projects/tt/members/bob", map[string]string{"role": "viewer"})
	b.must(200, "GET", "/api/v1/projects/tt/tasks", nil)
	b.must(403, "POST", "/api/v1/projects/tt/tasks", map[string]string{"title": "nope"})
	b.must(403, "PUT", "/api/v1/projects/tt/members/bob", map[string]string{"role": "owner"})

	// Alice creates an agent and a key for it.
	aRaw.must(201, "POST", "/api/v1/agents", map[string]string{"handle": "claude/opus4.7"})
	agentKey := aRaw.must(201, "POST", "/api/v1/keys", map[string]any{"agent": "claude/opus4.7", "name": "laptop"})["key"].(string)
	b.must(403, "POST", "/api/v1/keys", map[string]any{"agent": "claude/opus4.7"})
	ag := anon.as(agentKey)
	if who := ag.must(200, "GET", "/api/v1/whoami", nil); who["on_behalf_of"] != "alice" || who["admin"] != nil {
		t.Fatalf("agent whoami = %v", who)
	}
	ag.must(403, "POST", "/api/v1/keys", map[string]any{})

	// The agent's writes are attributed to it, whatever the client claims.
	parent := ag.must(201, "POST", "/api/v1/projects/tt/tasks", map[string]string{"title": "parent"})
	parentID := parent["id"].(string)
	if parent["created_by"] != "claude/opus4.7" {
		t.Errorf("created_by = %v", parent["created_by"])
	}
	child := ag.must(201, "POST", "/api/v1/projects/tt/tasks", map[string]any{"title": "child", "parent_id": parentID})
	childID := child["id"].(string)
	other := ag.must(201, "POST", "/api/v1/projects/tt/tasks", map[string]string{"title": "other"})
	otherID := other["id"].(string)
	cm := ag.must(201, "POST", "/api/v1/projects/tt/tasks/"+childID+"/comments",
		map[string]string{"body": "hi", "author": "mallory"})
	if cm["author"] != "claude/opus4.7" {
		t.Errorf("comment author = %v, want claude/opus4.7", cm["author"])
	}

	// Offline attenuation: comment-only, under parent, labelled.
	sub, err := auth.Attenuate(agentKey, auth.Restrictions{
		Tasks:   []string{parentID},
		Actions: []auth.Action{auth.ActTaskRead, auth.ActCommentCreate},
		Label:   "sub-reviewer",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := anon.as(sub)
	s.must(200, "GET", "/api/v1/projects/tt/tasks/"+otherID, nil)
	scm := s.must(201, "POST", "/api/v1/projects/tt/tasks/"+childID+"/comments", map[string]string{"body": "lgtm"})
	if scm["author"] != "claude/opus4.7 (sub-reviewer)" {
		t.Errorf("sub comment author = %v", scm["author"])
	}
	s.must(403, "POST", "/api/v1/projects/tt/tasks/"+otherID+"/comments", map[string]string{"body": "out of scope"})
	s.must(403, "PATCH", "/api/v1/projects/tt/tasks/"+childID, map[string]string{"status": "done"})
	s.must(403, "POST", "/api/v1/projects/tt/tasks", map[string]any{"title": "x", "parent_id": parentID})
	s.must(403, "POST", "/api/v1/keys", map[string]any{})

	// A scoped key with edit rights still can't move a task out of scope.
	editSub, _ := auth.Attenuate(agentKey, auth.Restrictions{Tasks: []string{parentID}})
	es := anon.as(editSub)
	es.must(200, "PATCH", "/api/v1/projects/tt/tasks/"+childID, map[string]string{"status": "in_progress"})
	es.must(403, "PATCH", "/api/v1/projects/tt/tasks/"+otherID, map[string]string{"status": "done"})
	es.must(403, "POST", "/api/v1/projects/tt/tasks", map[string]string{"title": "top-level"})

	// Revoking the agent's key kills the key, its JWTs and derived keys.
	agentTok := ag.must(200, "POST", "/api/v1/token", nil)["token"].(string)
	anon.as(agentTok).must(200, "GET", "/api/v1/whoami", nil)
	var keys struct {
		Keys []struct {
			ID     string `json:"id"`
			Handle string `json:"handle"`
		} `json:"keys"`
	}
	raw, _ := json.Marshal(aRaw.must(200, "GET", "/api/v1/keys", nil))
	json.Unmarshal(raw, &keys)
	var agentKeyID string
	for _, k := range keys.Keys {
		if k.Handle == "claude/opus4.7" {
			agentKeyID = k.ID
		}
	}
	b.must(404, "DELETE", "/api/v1/keys/"+agentKeyID, nil)
	aRaw.must(204, "DELETE", "/api/v1/keys/"+agentKeyID, nil)
	anon.as(agentTok).must(401, "GET", "/api/v1/whoami", nil)
	ag.must(401, "GET", "/api/v1/whoami", nil)
	s.must(401, "GET", "/api/v1/whoami", nil)

	// Lowering a role revokes outstanding JWTs; a fresh exchange works.
	aRaw.must(200, "PUT", "/api/v1/projects/tt/members/bob", map[string]string{"role": "member"})
	bobTok := b.must(200, "POST", "/api/v1/token", nil)["token"].(string)
	anon.as(bobTok).must(201, "POST", "/api/v1/projects/tt/tasks", map[string]string{"title": "by bob"})
	aRaw.must(200, "PUT", "/api/v1/projects/tt/members/bob", map[string]string{"role": "viewer"})
	anon.as(bobTok).must(401, "GET", "/api/v1/whoami", nil)
	bobTok = b.must(200, "POST", "/api/v1/token", nil)["token"].(string)
	anon.as(bobTok).must(403, "POST", "/api/v1/projects/tt/tasks", map[string]string{"title": "again"})

	// JWKS publishes the signing key.
	if jwks := anon.must(200, "GET", "/.well-known/jwks.json", nil); len(jwks["keys"].([]any)) != 1 {
		t.Errorf("jwks = %v", jwks)
	}

	// Every write above left an audit event with an actor.
	var events []struct {
		Action string  `db:"action"`
		Label  *string `db:"actor_label"`
	}
	if err := dir.db.Select(&events, `SELECT action, actor_label FROM task_events ORDER BY id`); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range events {
		s := e.Action
		if e.Label != nil {
			s += "(" + *e.Label + ")"
		}
		got = append(got, s)
	}
	want := "create create create comment comment(sub-reviewer) update create"
	if strings.Join(got, " ") != want {
		t.Errorf("audit events = %v\nwant %s", got, want)
	}
}

func TestServer_ProjectsAreIsolated(t *testing.T) {
	srv, hs := newTestServer(t)
	ctx := context.Background()
	dir := srv.Directory()
	alice, _ := dir.CreateUser(ctx, "alice", nil, true)
	_, key, _ := dir.CreateKey(ctx, alice, alice, "", nil, "", time.Time{})
	c := (&client{t: t, base: hs.URL}).as(key)

	c.must(201, "POST", "/api/v1/projects", map[string]string{"slug": "one"})
	c.must(201, "POST", "/api/v1/projects", map[string]string{"slug": "two"})
	id := c.must(201, "POST", "/api/v1/projects/one/tasks", map[string]string{"title": "in one"})["id"].(string)
	c.must(200, "GET", "/api/v1/projects/one/tasks/"+id, nil)
	c.must(404, "GET", "/api/v1/projects/two/tasks/"+id, nil)

	// A key scoped to project two can't see project one at all.
	scoped, _ := auth.Attenuate(key, auth.Restrictions{Projects: []string{"two"}})
	c.as(scoped).must(404, "GET", "/api/v1/projects/one/tasks", nil)
	c.as(scoped).must(200, "GET", "/api/v1/projects/two/tasks", nil)
	c.as(scoped).must(403, "POST", "/api/v1/projects", map[string]string{"slug": "three"})

	out := c.must(200, "GET", "/api/v1/projects", nil)
	if n := len(out["projects"].([]any)); n != 2 {
		t.Errorf("projects = %d, want 2", n)
	}
}

// TestServer_DeviceLogin drives the tt login flow: start, approve in a
// logged-in browser, poll for a key that then works.
func TestServer_DeviceLogin(t *testing.T) {
	srv, hs := newTestServer(t)
	ctx := context.Background()
	alice, _ := srv.Directory().CreateUser(ctx, "alice", nil, false)
	session, _ := srv.Directory().CreateSession(ctx, alice.ID, time.Hour)
	anon := &client{t: t, base: hs.URL}

	start := anon.must(200, "POST", "/auth/device/start", map[string]string{"client_name": "laptop"})
	device, userCode := start["device_code"].(string), start["user_code"].(string)
	if code, out := anon.do("POST", "/auth/device/token", map[string]string{"device_code": device}); code != 400 ||
		out["error"] != "authorization_pending" {
		t.Fatalf("poll before approval = %d %v", code, out)
	}

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	form := func(cookie bool, code string) int {
		req, _ := http.NewRequest("POST", hs.URL+"/auth/device/approve", strings.NewReader("user_code="+code))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if cookie {
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
		}
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	// Without a session the page redirects to login and approval is refused.
	resp, _ := noRedirect.Get(hs.URL + "/auth/device?user_code=" + userCode)
	if resp.StatusCode != http.StatusFound || !strings.HasPrefix(resp.Header.Get("Location"), "/auth/github/login?next=") {
		t.Errorf("device page without session = %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if code := form(false, userCode); code != 401 {
		t.Errorf("approve without session = %d, want 401", code)
	}
	if code := form(true, "WRONG-CODE"); code != 400 {
		t.Errorf("approve wrong code = %d, want 400", code)
	}
	if code := form(true, userCode); code != 200 {
		t.Fatalf("approve = %d", code)
	}

	got := anon.must(200, "POST", "/auth/device/token", map[string]string{"device_code": device})
	key := got["key"].(string)
	if who := anon.as(key).must(200, "GET", "/api/v1/whoami", nil); who["handle"] != "alice" {
		t.Errorf("whoami with login key = %v", who)
	}
	// The device code is single-use.
	anon.must(400, "POST", "/auth/device/token", map[string]string{"device_code": device})
}

func TestServer_ImportSQLite(t *testing.T) {
	srv, hs := newTestServer(t)
	ctx := context.Background()
	dir := srv.Directory()
	alice, _ := dir.CreateUser(ctx, "alice", nil, true)
	proj, err := dir.CreateProject(ctx, "tt", "tt", alice)
	if err != nil {
		t.Fatal(err)
	}

	path := t.TempDir() + "/local.db"
	local, err := ttdb.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	by := "someone"
	child, _ := task.Create(local, "child", task.CreateOpts{CreatedBy: &by})
	parent, _ := task.Create(local, "parent", task.CreateOpts{})
	task.Update(local, child.ID, task.UpdateOpts{ParentID: &parent.ID})
	task.AddComment(local, child.ID, "note", &by)
	task.AddRelation(local, parent.ID, "blocks", child.ID)
	local.Close()

	for run, want := range []ImportStats{{2, 1, 1, 0}, {0, 0, 0, 0}} {
		st, err := dir.ImportSQLite(ctx, proj, path, alice)
		if err != nil {
			t.Fatalf("import run %d: %v", run, err)
		}
		if st != want {
			t.Errorf("import run %d = %+v, want %+v", run, st, want)
		}
	}

	_, key, _ := dir.CreateKey(ctx, alice, alice, "", nil, "", time.Time{})
	c := (&client{t: t, base: hs.URL}).as(key)
	got := c.must(200, "GET", "/api/v1/projects/tt/tasks/"+child.ID, nil)
	tk := got["task"].(map[string]any)
	if tk["parent_id"] != parent.ID || tk["created_by"] != "someone" || tk["created_at"] != child.CreatedAt {
		t.Errorf("imported task = %v", tk)
	}
	if len(got["comments"].([]any)) != 1 || len(got["relations"].([]any)) != 1 {
		t.Errorf("imported comments/relations = %v / %v", got["comments"], got["relations"])
	}
}

// TestServer_WebUISessionFlow follows what the web UI's JavaScript does:
// load the page, trade the session cookie for a JWT, list projects.
func TestServer_WebUISessionFlow(t *testing.T) {
	srv, hs := newTestServer(t)
	ctx := context.Background()
	alice, _ := srv.Directory().CreateUser(ctx, "alice", nil, true)
	srv.Directory().CreateProject(ctx, "tt", "tt", alice)
	session, _ := srv.Directory().CreateSession(ctx, alice.ID, time.Hour)

	resp, err := http.Get(hs.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), "const TT_SERVER = true;") {
		t.Error("server UI is not configured for server mode")
	}

	anon := &client{t: t, base: hs.URL}
	anon.must(401, "POST", "/api/v1/token", nil)
	req, _ := http.NewRequest("POST", hs.URL+"/api/v1/token", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var tok struct {
		Token string `json:"token"`
	}
	json.NewDecoder(resp.Body).Decode(&tok)
	resp.Body.Close()
	if tok.Token == "" {
		t.Fatalf("no token from session, status %d", resp.StatusCode)
	}
	if out := anon.as(tok.Token).must(200, "GET", "/api/v1/projects", nil); len(out["projects"].([]any)) != 1 {
		t.Errorf("projects = %v", out)
	}
	// The API itself never accepts the cookie, only the bearer token.
	req, _ = http.NewRequest("GET", hs.URL+"/api/v1/projects", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("API with cookie only = %d, want 401", resp.StatusCode)
	}
}
