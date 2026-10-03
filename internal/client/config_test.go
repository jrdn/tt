package client

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

// isolate points the tt config dir at a temp dir and resets TT_ vars.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("TT_API_KEY", "")
	t.Setenv("TT_KEYRING", "")
	path, err := credentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const srv = "https://tt.example.com"

func TestCredential_StoredInKeychain(t *testing.T) {
	keyring.MockInit()
	path := isolate(t)

	where, err := SaveCredential(srv+"/", Credential{Key: "tt_ak_secret", Handle: "jrdn"})
	if err != nil {
		t.Fatal(err)
	}
	if where != "the OS keychain" {
		t.Errorf("stored in %q", where)
	}
	if f := readFile(t, path); strings.Contains(f, "tt_ak_secret") || !strings.Contains(f, `"storage": "keychain"`) {
		t.Errorf("credentials file holds the key or lacks the marker:\n%s", f)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0600 {
		t.Errorf("credentials file mode = %v", fi.Mode().Perm())
	}
	if key, err := KeyFor(srv); err != nil || key != "tt_ak_secret" {
		t.Errorf("KeyFor = %q, %v", key, err)
	}

	// Logout removes it from both places.
	if _, err := SaveCredential(srv, Credential{}); err != nil {
		t.Fatal(err)
	}
	if _, err := keyring.Get(keyringService, srv); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("keychain entry after logout: %v", err)
	}
	if _, err := KeyFor(srv); err == nil {
		t.Error("expected not logged in after logout")
	}
}

func TestCredential_FallsBackToFile(t *testing.T) {
	keyring.MockInitWithError(errors.New("no secret service"))
	path := isolate(t)

	where, err := SaveCredential(srv, Credential{Key: "tt_ak_secret"})
	if err != nil {
		t.Fatal(err)
	}
	if where != path || !strings.Contains(readFile(t, path), "tt_ak_secret") {
		t.Errorf("fallback stored in %q", where)
	}
	if key, _ := KeyFor(srv); key != "tt_ak_secret" {
		t.Errorf("KeyFor = %q", key)
	}
}

func TestCredential_KeyringOff(t *testing.T) {
	keyring.MockInit()
	path := isolate(t)
	t.Setenv("TT_KEYRING", "off")

	if where, _ := SaveCredential(srv, Credential{Key: "tt_ak_secret"}); where != path {
		t.Errorf("TT_KEYRING=off stored in %q", where)
	}
	if _, err := keyring.Get(keyringService, srv); !errors.Is(err, keyring.ErrNotFound) {
		t.Error("TT_KEYRING=off still wrote to the keychain")
	}
}

// Keys saved in plain text by older versions move to the keychain on use.
func TestCredential_MigratesPlaintext(t *testing.T) {
	keyring.MockInit()
	path := isolate(t)
	t.Setenv("TT_KEYRING", "off")
	SaveCredential(srv, Credential{Key: "tt_ak_old", Handle: "jrdn"})
	t.Setenv("TT_KEYRING", "")

	if key, err := KeyFor(srv); err != nil || key != "tt_ak_old" {
		t.Fatalf("KeyFor = %q, %v", key, err)
	}
	if f := readFile(t, path); strings.Contains(f, "tt_ak_old") {
		t.Errorf("plaintext key still in file after migration:\n%s", f)
	}
	if key, _ := keyring.Get(keyringService, srv); key != "tt_ak_old" {
		t.Errorf("keychain = %q", key)
	}
}

func TestCredential_EnvWins(t *testing.T) {
	keyring.MockInit()
	isolate(t)
	SaveCredential(srv, Credential{Key: "tt_ak_stored"})
	t.Setenv("TT_API_KEY", "tt_ak_env")
	if key, _ := KeyFor(srv); key != "tt_ak_env" {
		t.Errorf("KeyFor = %q, want TT_API_KEY", key)
	}
}

// A keychain that never answers (locked, prompting over SSH) must not hang
// tt: saving falls back to the file, reading fails with advice.
func TestCredential_StuckKeychainTimesOut(t *testing.T) {
	keyring.MockInit()
	path := isolate(t)
	SaveCredential(srv, Credential{Key: "tt_ak_secret"}) // now in the keychain

	block := make(chan struct{})
	defer close(block)
	origSet, origGet, origTimeout := keyringSet, keyringGet, keyringTimeout
	defer func() { keyringSet, keyringGet, keyringTimeout = origSet, origGet, origTimeout }()
	keyringSet = func(string, string, string) error { <-block; return nil }
	keyringGet = func(string, string) (string, error) { <-block; return "", nil }
	keyringTimeout = 50 * time.Millisecond

	if _, err := KeyFor(srv); err == nil || !strings.Contains(err.Error(), "did not respond") {
		t.Errorf("KeyFor with stuck keychain = %v", err)
	}
	if where, err := SaveCredential(srv, Credential{Key: "tt_ak_new"}); err != nil || where != path {
		t.Errorf("SaveCredential with stuck keychain stored in %q, %v; want file fallback", where, err)
	}
}
