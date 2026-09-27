package auth

import (
	"slices"
	"strings"
	"testing"
	"time"
)

var master = []byte("0123456789abcdef0123456789abcdef")

func TestIssuer_SignVerify(t *testing.T) {
	iss, err := NewIssuer(master, "tt-test", time.Hour, 1)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := iss.Sign(&Claims{
		Kind:     KindAgent,
		Handle:   "claude/opus4.7",
		Projects: map[string]Role{"tt": RoleMember},
		Actions:  []Action{ActTaskRead},
	}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	c, err := iss.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if c.Handle != "claude/opus4.7" || c.Projects["tt"] != RoleMember || c.ID == "" {
		t.Errorf("claims not round-tripped: %+v", c)
	}
}

func TestIssuer_RejectsExpiredAndForeign(t *testing.T) {
	iss, _ := NewIssuer(master, "tt-test", time.Hour, 1)
	tok, _ := iss.Sign(&Claims{Handle: "x"}, time.Time{})

	iss.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if _, err := iss.Verify(tok); err == nil {
		t.Error("expected expired token to fail")
	}

	other, _ := NewIssuer([]byte("ffffffffffffffffffffffffffffffff"), "tt-test", time.Hour, 1)
	if _, err := other.Verify(tok); err == nil {
		t.Error("expected token from another master key to fail")
	}
}

func TestIssuer_NotAfterCapsExpiry(t *testing.T) {
	iss, _ := NewIssuer(master, "tt-test", time.Hour, 1)
	cap := time.Now().Add(10 * time.Minute).Truncate(time.Second)
	tok, _ := iss.Sign(&Claims{Handle: "x"}, cap)
	c, err := iss.Verify(tok)
	if err != nil {
		t.Fatal(err)
	}
	if !c.ExpiresAt.Time.Equal(cap) {
		t.Errorf("exp = %v, want %v", c.ExpiresAt.Time, cap)
	}
}

func TestIssuer_OldVersionStillVerifies(t *testing.T) {
	v1, _ := NewIssuer(master, "tt-test", time.Hour, 1)
	tok, _ := v1.Sign(&Claims{Handle: "x"}, time.Time{})
	v2, _ := NewIssuer(master, "tt-test", time.Hour, 2)
	if _, err := v2.Verify(tok); err != nil {
		t.Errorf("v1 token should verify after rotation: %v", err)
	}
	if len(v2.JWKS()["keys"].([]map[string]string)) != 2 {
		t.Error("JWKS should publish both versions")
	}
}

func TestKey_MintVerifyUnrestricted(t *testing.T) {
	key, err := MintKey(master, "key123")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, KeyPrefix) {
		t.Errorf("key %q lacks prefix", key)
	}
	id, r, err := VerifyKey(master, key, time.Now())
	if err != nil {
		t.Fatalf("VerifyKey: %v", err)
	}
	if id != "key123" || r.Projects != nil || r.Actions != nil || r.MaxRole != "" {
		t.Errorf("unexpected: id=%q r=%+v", id, r)
	}
}

func TestKey_AttenuateOffline(t *testing.T) {
	root, _ := MintKey(master, "key123")
	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	sub, err := Attenuate(root, Restrictions{
		Projects: []string{"tt"},
		MaxRole:  RoleViewer,
		Tasks:    []string{"ab12cde"},
		Actions:  []Action{ActTaskRead, ActCommentCreate},
		Expires:  exp,
		Label:    "sub-reviewer",
	})
	if err != nil {
		t.Fatal(err)
	}
	id, r, err := VerifyKey(master, sub, time.Now())
	if err != nil {
		t.Fatalf("VerifyKey: %v", err)
	}
	if id != "key123" {
		t.Errorf("id = %q", id)
	}
	if !slices.Equal(r.Projects, []string{"tt"}) || r.MaxRole != RoleViewer ||
		!slices.Equal(r.Tasks, []string{"ab12cde"}) || !r.Expires.Equal(exp) || r.Label != "sub-reviewer" ||
		!slices.Equal(r.Actions, []Action{ActTaskRead, ActCommentCreate}) {
		t.Errorf("restrictions = %+v", r)
	}
}

// Attenuating twice can only narrow further, never widen.
func TestKey_NestedAttenuationOnlyNarrows(t *testing.T) {
	root, _ := MintKey(master, "k")
	a, _ := Attenuate(root, Restrictions{
		Projects: []string{"tt", "web"},
		MaxRole:  RoleMember,
		Actions:  []Action{ActTaskRead, ActCommentCreate},
		Expires:  time.Now().Add(time.Hour),
		Label:    "worker",
	})
	b, _ := Attenuate(a, Restrictions{
		Projects: []string{"web", "other"},
		MaxRole:  RoleOwner,
		Actions:  []Action{ActCommentCreate, ActTaskEdit},
		Expires:  time.Now().Add(48 * time.Hour),
		Label:    "helper",
	})
	_, r, err := VerifyKey(master, b, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.Projects, []string{"web"}) {
		t.Errorf("projects = %v, want [web]", r.Projects)
	}
	if r.MaxRole != RoleMember {
		t.Errorf("role = %v, want member", r.MaxRole)
	}
	if !slices.Equal(r.Actions, []Action{ActCommentCreate}) {
		t.Errorf("actions = %v, want [comment:create]", r.Actions)
	}
	if r.Expires.After(time.Now().Add(time.Hour)) {
		t.Errorf("expires = %v, should keep the earlier limit", r.Expires)
	}
	if r.Label != "worker/helper" {
		t.Errorf("label = %q", r.Label)
	}
}

func TestKey_Rejections(t *testing.T) {
	root, _ := MintKey(master, "k")
	expired, _ := Attenuate(root, Restrictions{Expires: time.Now().Add(-time.Minute)})
	noActions, _ := Attenuate(root, Restrictions{Actions: []Action{ActTaskRead}})
	noActions, _ = Attenuate(noActions, Restrictions{Actions: []Action{ActTaskEdit}})
	noProjects, _ := Attenuate(root, Restrictions{Projects: []string{"a"}})
	noProjects, _ = Attenuate(noProjects, Restrictions{Projects: []string{"b"}})

	// Tamper: overwrite the tail of the encoded key, where the signature lives.
	tampered := root[:len(root)-4] + "AAAA"

	cases := map[string]string{
		"expired":      expired,
		"no actions":   noActions,
		"no projects":  noProjects,
		"tampered":     tampered,
		"not a key":    "ghp_abc",
		"other master": mustMint(t, []byte("ffffffffffffffffffffffffffffffff"), "k"),
	}
	for name, key := range cases {
		if _, _, err := VerifyKey(master, key, time.Now()); err == nil {
			t.Errorf("%s: expected VerifyKey to fail", name)
		}
	}
}

func mustMint(t *testing.T, m []byte, id string) string {
	k, err := MintKey(m, id)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestClaims_Can(t *testing.T) {
	c := &Claims{Projects: map[string]Role{"tt": RoleMember, "web": RoleViewer}}
	if !c.Can("tt", ActTaskEdit) {
		t.Error("member should edit")
	}
	if c.Can("tt", ActProjectAdmin) {
		t.Error("member should not admin")
	}
	if c.Can("web", ActTaskCreate) {
		t.Error("viewer should not create")
	}
	if c.Can("other", ActTaskRead) {
		t.Error("no access to unlisted project")
	}
	c.Actions = []Action{ActTaskRead}
	if c.Can("tt", ActTaskEdit) {
		t.Error("action list should restrict below role")
	}
	if !c.Can("tt", ActTaskRead) {
		t.Error("listed action should be allowed")
	}
}
