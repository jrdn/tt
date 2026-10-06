//go:build integration

package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	if who := ag.must(200, "GET", "/api/v1/whoami", nil); who["on_behalf_of"] != "alice" || who["admin"] != nil ||
		who["projects"].(map[string]any)["tt"] != "member" {
		t.Fatalf("agent whoami = %v", who)
	}
	ag.must(403, "POST", "/api/v1/keys", map[string]any{})
	ag.must(403, "PUT", "/api/v1/projects/tt/members/bob", map[string]string{"role": "owner"})

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

// browser makes requests as a logged-in browser: session cookie, no
// automatic redirects.
type browser struct {
	t       *testing.T
	base    string
	session string
	origin  string
}

func (b *browser) do(method, path string, form url.Values) (*http.Response, string) {
	b.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, b.base+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if b.session != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: b.session})
	}
	if b.origin != "" {
		req.Header.Set("Origin", b.origin)
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, string(data)
}

func pkce() (verifier, challenge string) {
	verifier = "verifier-" + task.NewID() + task.NewID() + task.NewID() + task.NewID() + task.NewID()
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

// TestServer_CLILogin covers the loopback login: only loopback redirects,
// approval needs a same-origin logged-in browser, and a code is redeemable
// once, with the right verifier and redirect_uri.
func TestServer_CLILogin(t *testing.T) {
	srv, hs := newTestServer(t)
	ctx := context.Background()
	alice, _ := srv.Directory().CreateUser(ctx, "alice", nil, false)
	session, _ := srv.Directory().CreateSession(ctx, alice.ID, time.Hour)
	anon := &client{t: t, base: hs.URL}
	b := &browser{t: t, base: hs.URL, session: session}
	verifier, challenge := pkce()
	const redirect = "http://127.0.0.1:5555/callback"
	params := func(redirect string) url.Values {
		return url.Values{"redirect_uri": {redirect}, "state": {"st8"}, "code_challenge": {challenge},
			"code_challenge_method": {"S256"}, "client_name": {"laptop"}}
	}

	for _, bad := range []string{"http://evil.example/callback", "https://127.0.0.1:5555/callback",
		"http://127.0.0.1.evil.example:5555/callback", "http://127.0.0.1:5555/other", "http://127.0.0.1/callback",
		"http://localhost:5555/callback", "http://user@127.0.0.1:5555/callback", "http://127.0.0.1:5555/callback?x=1"} {
		if resp, _ := b.do("GET", "/auth/cli?"+params(bad).Encode(), nil); resp.StatusCode != 400 {
			t.Errorf("redirect_uri %s accepted (%d)", bad, resp.StatusCode)
		}
		if resp, _ := b.do("POST", "/auth/cli/approve", params(bad)); resp.StatusCode != 400 {
			t.Errorf("approve with redirect_uri %s accepted (%d)", bad, resp.StatusCode)
		}
	}
	if resp, _ := (&browser{t: t, base: hs.URL}).do("GET", "/auth/cli?"+params(redirect).Encode(), nil); resp.StatusCode != 302 ||
		!strings.HasPrefix(resp.Header.Get("Location"), "/auth/github/login?next=") {
		t.Errorf("approval page without session = %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, page := b.do("GET", "/auth/cli?"+params(redirect).Encode(), nil); resp.StatusCode != 200 ||
		!strings.Contains(page, "alice") || !strings.Contains(page, "laptop") {
		t.Errorf("approval page = %d\n%s", resp.StatusCode, page)
	}
	if resp, _ := (&browser{t: t, base: hs.URL}).do("POST", "/auth/cli/approve", params(redirect)); resp.StatusCode != 401 {
		t.Errorf("approve without session = %d", resp.StatusCode)
	}
	evil := &browser{t: t, base: hs.URL, session: session, origin: "https://evil.example"}
	if resp, _ := evil.do("POST", "/auth/cli/approve", params(redirect)); resp.StatusCode != 403 {
		t.Errorf("cross-origin approve = %d", resp.StatusCode)
	}

	approve := func() string {
		t.Helper()
		resp, _ := b.do("POST", "/auth/cli/approve", params(redirect))
		loc, _ := url.Parse(resp.Header.Get("Location"))
		if resp.StatusCode != 302 || loc == nil || !strings.HasPrefix(loc.String(), redirect+"?") || loc.Query().Get("state") != "st8" {
			t.Fatalf("approve = %d %s", resp.StatusCode, resp.Header.Get("Location"))
		}
		return loc.Query().Get("code")
	}
	redeem := func(code, verifier, redirect string) (int, map[string]any) {
		return anon.do("POST", "/auth/cli/token", map[string]string{"code": code, "code_verifier": verifier, "redirect_uri": redirect})
	}

	// A wrong verifier fails and burns the code.
	code := approve()
	if c, _ := redeem(code, "wrong", redirect); c != 400 {
		t.Errorf("wrong verifier = %d", c)
	}
	if c, _ := redeem(code, verifier, redirect); c != 400 {
		t.Errorf("code reusable after a failed attempt (%d)", c)
	}
	if c, _ := redeem(approve(), verifier, "http://127.0.0.1:6666/callback"); c != 400 {
		t.Errorf("mismatched redirect_uri = %d", c)
	}

	code = approve()
	c, out := redeem(code, verifier, redirect)
	if c != 200 {
		t.Fatalf("redeem = %d %v", c, out)
	}
	if who := anon.as(out["key"].(string)).must(200, "GET", "/api/v1/whoami", nil); who["handle"] != "alice" {
		t.Errorf("whoami = %v", who)
	}
	if c, _ := redeem(code, verifier, redirect); c != 400 {
		t.Errorf("code redeemed twice (%d)", c)
	}
}

// TestServer_DeviceLogin covers `tt login --device`: the code must be typed
// (a code in the URL is ignored), and the confirmation names the machine.
func TestServer_DeviceLogin(t *testing.T) {
	srv, hs := newTestServer(t)
	ctx := context.Background()
	alice, _ := srv.Directory().CreateUser(ctx, "alice", nil, false)
	session, _ := srv.Directory().CreateSession(ctx, alice.ID, time.Hour)
	anon := &client{t: t, base: hs.URL}
	b := &browser{t: t, base: hs.URL, session: session}

	start := anon.must(200, "POST", "/auth/device/start", map[string]string{"client_name": "laptop"})
	device, userCode := start["device_code"].(string), start["user_code"].(string)
	if _, ok := start["verification_uri_complete"]; ok {
		t.Error("start response includes a URL with the code filled in")
	}
	if code, out := anon.do("POST", "/auth/device/token", map[string]string{"device_code": device}); code != 400 ||
		out["error"] != "authorization_pending" {
		t.Fatalf("poll before approval = %d %v", code, out)
	}

	// A link with the code in it gets only the blank form.
	if resp, page := b.do("GET", "/auth/device?user_code="+userCode, nil); resp.StatusCode != 200 ||
		strings.Contains(page, userCode) || strings.Contains(page, "Approve") {
		t.Errorf("device page used the code from the URL:\n%s", page)
	}
	if resp, _ := (&browser{t: t, base: hs.URL}).do("GET", "/auth/device", nil); resp.StatusCode != 302 {
		t.Errorf("device page without session = %d", resp.StatusCode)
	}

	// Typed loosely (lower case, no dash), the code finds the request.
	typed := strings.ToLower(strings.ReplaceAll(userCode, "-", ""))
	if resp, page := b.do("POST", "/auth/device/confirm", url.Values{"user_code": {typed}}); resp.StatusCode != 200 ||
		!strings.Contains(page, "laptop") || !strings.Contains(page, "Approve") {
		t.Errorf("confirm = %d\n%s", resp.StatusCode, page)
	}
	if resp, _ := b.do("POST", "/auth/device/confirm", url.Values{"user_code": {"WRNG-CODE"}}); resp.StatusCode != 400 {
		t.Errorf("confirm with wrong code = %d", resp.StatusCode)
	}
	evil := &browser{t: t, base: hs.URL, session: session, origin: "https://evil.example"}
	if resp, _ := evil.do("POST", "/auth/device/approve", url.Values{"user_code": {userCode}}); resp.StatusCode != 403 {
		t.Errorf("cross-origin approve = %d", resp.StatusCode)
	}
	if resp, _ := (&browser{t: t, base: hs.URL}).do("POST", "/auth/device/approve", url.Values{"user_code": {userCode}}); resp.StatusCode != 401 {
		t.Errorf("approve without session = %d", resp.StatusCode)
	}
	if resp, _ := b.do("POST", "/auth/device/approve", url.Values{"user_code": {typed}}); resp.StatusCode != 200 {
		t.Fatalf("approve = %d", resp.StatusCode)
	}

	got := anon.must(200, "POST", "/auth/device/token", map[string]string{"device_code": device})
	if who := anon.as(got["key"].(string)).must(200, "GET", "/api/v1/whoami", nil); who["handle"] != "alice" {
		t.Errorf("whoami with login key = %v", who)
	}
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

// sse opens an event stream and returns received "data:" payloads plus a
// channel closed when the stream ends.
func sse(t *testing.T, url, token string) (<-chan string, <-chan struct{}, int) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, nil, resp.StatusCode
	}
	data, done := make(chan string, 16), make(chan struct{})
	go func() {
		defer close(done)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if d, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				data <- d
			}
		}
	}()
	t.Cleanup(func() { resp.Body.Close() })
	return data, done, 200
}

func TestServer_LiveEvents(t *testing.T) {
	liveRecheck = 100 * time.Millisecond
	srv, hs := newTestServer(t)
	ctx := context.Background()
	dir := srv.Directory()
	alice, _ := dir.CreateUser(ctx, "alice", nil, true)
	bob, _ := dir.CreateUser(ctx, "bob", nil, false)
	carol, _ := dir.CreateUser(ctx, "carol", nil, false)
	tt, _ := dir.CreateProject(ctx, "tt", "tt", alice)
	dir.CreateProject(ctx, "other", "other", alice)
	dir.SetMember(ctx, tt, bob, auth.RoleViewer)
	_, aliceKey, _ := dir.CreateKey(ctx, alice, alice, "", nil, "", time.Time{})
	bobRec, bobKey, _ := dir.CreateKey(ctx, bob, bob, "", nil, "", time.Time{})
	_, carolKey, _ := dir.CreateKey(ctx, carol, carol, "", nil, "", time.Time{})
	a := (&client{t: t, base: hs.URL}).as(aliceKey)

	if _, _, code := sse(t, hs.URL+"/api/v1/projects/tt/events", carolKey); code != 404 {
		t.Errorf("non-member events = %d, want 404", code)
	}
	events, done, code := sse(t, hs.URL+"/api/v1/projects/tt/events", bobKey)
	if code != 200 {
		t.Fatalf("viewer events = %d", code)
	}

	a.must(201, "POST", "/api/v1/projects/other/tasks", map[string]string{"title": "elsewhere"})
	id := a.must(201, "POST", "/api/v1/projects/tt/tasks", map[string]string{"title": "live"})["id"].(string)
	select {
	case d := <-events:
		if !strings.Contains(d, id) || !strings.Contains(d, `"action":"create"`) {
			t.Errorf("event = %s, want create of %s (other projects must not leak in)", d, id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no live event after create")
	}

	// Revoking bob's key ends his open stream.
	a.must(404, "DELETE", "/api/v1/keys/"+bobRec.ID, nil) // not alice's key
	dir.RevokeKey(ctx, bobRec.ID)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream stayed open after the key was revoked")
	}
}

// Operator-created users attach to a GitHub account only when explicitly
// linked; otherwise a colliding GitHub login gets a clear conflict.
func TestServer_GitHubLinking(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	dir := srv.Directory()

	// Linked (case-insensitively): the first sign-in reuses the user, so
	// keys minted before the sign-in keep working.
	jrdn, _ := dir.CreateUser(ctx, "jrdn", nil, false)
	if err := dir.LinkGitHub(ctx, "jrdn", "JRDN"); err != nil {
		t.Fatal(err)
	}
	_, key, _ := dir.CreateKey(ctx, jrdn, jrdn, "", nil, "", time.Time{})
	p, err := dir.UpsertGitHubUser(ctx, 42, "jrdn", "j@example.com", "J", true)
	if err != nil || p.ID != jrdn.ID {
		t.Fatalf("sign-in with linked login = %v, %v; want user %s", p, err, jrdn.ID)
	}
	if admin, _ := dir.IsAdmin(ctx, jrdn.ID); !admin {
		t.Error("TT_ADMINS promotion lost on link")
	}
	if _, _, err := dir.ClaimsForKey(ctx, key, time.Now()); err != nil {
		t.Errorf("key from before the link stopped working: %v", err)
	}
	// Later sign-ins find the user by GitHub ID, even after a rename.
	if p, err := dir.UpsertGitHubUser(ctx, 42, "jrdn-renamed", "", "", false); err != nil || p.ID != jrdn.ID {
		t.Errorf("sign-in after GitHub rename = %v, %v", p, err)
	}
	if err := dir.LinkGitHub(ctx, "jrdn", "someone"); err == nil {
		t.Error("relinking a user who already signed in with GitHub should fail")
	}

	// Not linked: a GitHub user with the same login is refused, not merged.
	dir.CreateUser(ctx, "bob", nil, false)
	var before, after int
	dir.db.Get(&before, `SELECT count(*) FROM users`)
	_, err = dir.UpsertGitHubUser(ctx, 43, "bob", "", "", false)
	if !errors.Is(err, errHandleTaken) || !strings.Contains(err.Error(), "tt server link-github bob bob") {
		t.Errorf("colliding sign-in error = %v", err)
	}
	dir.db.Get(&after, `SELECT count(*) FROM users`)
	if after != before {
		t.Errorf("colliding sign-in created a user (%d -> %d)", before, after)
	}
	// Linking bob fixes it.
	if err := dir.LinkGitHub(ctx, "bob", "bob"); err != nil {
		t.Fatal(err)
	}
	if _, err := dir.UpsertGitHubUser(ctx, 43, "bob", "", "", false); err != nil {
		t.Errorf("sign-in after link = %v", err)
	}

	// New logins still get new accounts.
	if p, err := dir.UpsertGitHubUser(ctx, 44, "carol", "", "", false); err != nil || p.Handle != "carol" {
		t.Errorf("new GitHub user = %v, %v", p, err)
	}

	// Link validation.
	dir.CreateUser(ctx, "dave", nil, false)
	if err := dir.LinkGitHub(ctx, "dave", "Carol"); err == nil {
		t.Error("linked a login that already belongs to another user")
	}
	owner, _ := dir.PrincipalByHandle(ctx, "carol")
	dir.CreateAgent(ctx, owner, "bot/1")
	if err := dir.LinkGitHub(ctx, "bot/1", "whatever"); err == nil {
		t.Error("linked GitHub to an agent")
	}
}

// Attenuated keys are enforced by the server: role cap, expiry, task scope
// across every write path, and loss of admin rights.
func TestServer_AttenuatedKeys(t *testing.T) {
	srv, hs := newTestServer(t)
	ctx := context.Background()
	dir := srv.Directory()
	anon := &client{t: t, base: hs.URL}

	alice, _ := dir.CreateUser(ctx, "alice", nil, true)
	_, aliceKey, _ := dir.CreateKey(ctx, alice, alice, "", nil, "", time.Time{})
	aRaw := anon.as(aliceKey)
	aRaw.must(201, "POST", "/api/v1/projects", map[string]string{"slug": "tt", "name": "tt"})
	aRaw.must(201, "POST", "/api/v1/agents", map[string]string{"handle": "claude/opus4.7"})
	agentKey := aRaw.must(201, "POST", "/api/v1/keys", map[string]any{"agent": "claude/opus4.7"})["key"].(string)
	ag := anon.as(agentKey)

	const tasks = "/api/v1/projects/tt/tasks"
	mk := func(title, parent string) string {
		body := map[string]any{"title": title}
		if parent != "" {
			body["parent_id"] = parent
		}
		return ag.must(201, "POST", tasks, body)["id"].(string)
	}
	parent := mk("parent", "")
	child := mk("child", parent)
	grandchild := mk("grandchild", child)
	sibling := mk("sibling", parent)
	other := mk("other", "")

	attenuate := func(r auth.Restrictions) *client {
		k, err := auth.Attenuate(agentKey, r)
		if err != nil {
			t.Fatal(err)
		}
		return anon.as(k)
	}

	// A viewer cap blocks writes the agent's member role would allow.
	viewer := attenuate(auth.Restrictions{MaxRole: auth.RoleViewer})
	viewer.must(200, "GET", tasks+"/"+other, nil)
	viewer.must(403, "POST", tasks, map[string]string{"title": "nope"})
	viewer.must(403, "PATCH", tasks+"/"+other, map[string]string{"status": "done"})

	// Expiry: an expired key is refused, and a live one caps its JWT's lifetime.
	attenuate(auth.Restrictions{Expires: time.Now().Add(-time.Minute)}).must(401, "GET", "/api/v1/whoami", nil)
	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	short := attenuate(auth.Restrictions{Expires: exp})
	tok := short.must(200, "POST", "/api/v1/token", nil)
	at, err := time.Parse(time.RFC3339, tok["expires_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if at.After(exp) {
		t.Errorf("token expires_at = %v, want no later than key expiry %v", at, exp)
	}

	// Several task roots: a write must fall under every one of them.
	multi := attenuate(auth.Restrictions{Tasks: []string{parent, child}})
	multi.must(200, "PATCH", tasks+"/"+grandchild, map[string]string{"status": "in_progress"})
	multi.must(200, "PATCH", tasks+"/"+child, map[string]string{"status": "in_progress"})
	multi.must(403, "PATCH", tasks+"/"+sibling, map[string]string{"status": "in_progress"})
	multi.must(201, "POST", tasks, map[string]any{"title": "new", "parent_id": grandchild})
	multi.must(403, "POST", tasks, map[string]any{"title": "new", "parent_id": sibling})

	scoped := attenuate(auth.Restrictions{Tasks: []string{parent}})

	// Moving a task out of scope, by reparenting it, is refused.
	scoped.must(403, "PATCH", tasks+"/"+child, map[string]any{"parent_id": other})

	// Relations need both ends in scope.
	scoped.must(403, "POST", tasks+"/"+child+"/relations", map[string]string{"type": "related", "target_id": other})
	scoped.must(403, "POST", tasks+"/"+other+"/relations", map[string]string{"type": "related", "target_id": child})
	if code, out := scoped.do("POST", tasks+"/"+child+"/relations", map[string]string{"type": "related", "target_id": sibling}); code >= 300 {
		t.Errorf("relation between in-scope tasks = %d: %v", code, out)
	}
	scoped.must(403, "DELETE", tasks+"/"+child+"/relations/related/"+other, nil)

	// Scoped keys can't pull "next" from the whole project.
	scoped.must(403, "POST", "/api/v1/projects/tt/next", map[string]string{"handle": "claude/opus4.7"})

	// Without task:read there's no event stream.
	commentOnly := attenuate(auth.Restrictions{Actions: []auth.Action{auth.ActCommentCreate}})
	commentOnly.must(403, "GET", "/api/v1/projects/tt/events", nil)
	commentOnly.must(403, "GET", tasks, nil)

	// A user's own admin rights don't survive attenuation.
	narrowed, err := auth.Attenuate(aliceKey, auth.Restrictions{Label: "helper"})
	if err != nil {
		t.Fatal(err)
	}
	anon.as(narrowed).must(403, "POST", "/api/v1/projects", map[string]string{"slug": "two"})
	aRaw.must(201, "POST", "/api/v1/projects", map[string]string{"slug": "two"})
}

// A key narrowed only by project, role or expiry must not be able to mint a
// fresh unrestricted key and shed those limits.
func TestServer_AttenuatedKeyCannotMintKeys(t *testing.T) {
	srv, hs := newTestServer(t)
	ctx := context.Background()
	anon := &client{t: t, base: hs.URL}

	alice, _ := srv.Directory().CreateUser(ctx, "alice", nil, true)
	_, aliceKey, _ := srv.Directory().CreateKey(ctx, alice, alice, "", nil, "", time.Time{})
	anon.as(aliceKey).must(201, "POST", "/api/v1/projects", map[string]string{"slug": "tt", "name": "tt"})

	cases := map[string]auth.Restrictions{
		"project": {Projects: []string{"tt"}},
		"role":    {MaxRole: auth.RoleViewer},
		"expires": {Expires: time.Now().Add(time.Hour)},
	}
	for name, r := range cases {
		narrowed, err := auth.Attenuate(aliceKey, r)
		if err != nil {
			t.Fatal(err)
		}
		c := anon.as(narrowed)
		c.must(403, "POST", "/api/v1/keys", map[string]any{})
		c.must(403, "POST", "/api/v1/agents", map[string]string{"handle": "sneaky-" + name})
	}

	// Limits stored on the key record count too, not just caveats.
	_, scopedKey, _ := srv.Directory().CreateKey(ctx, alice, alice, "", []string{"tt"}, "", time.Time{})
	anon.as(scopedKey).must(403, "POST", "/api/v1/keys", map[string]any{})
	_, cappedKey, _ := srv.Directory().CreateKey(ctx, alice, alice, "", nil, auth.RoleViewer, time.Time{})
	anon.as(cappedKey).must(403, "POST", "/api/v1/keys", map[string]any{})
}
