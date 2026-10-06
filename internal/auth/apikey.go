package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"gopkg.in/macaroon.v2"
)

// KeyPrefix marks tt API keys so secret scanners can find leaked ones.
const KeyPrefix = "tt_ak_"

const keyLocation = "tt"

// Restrictions are the limits carried by an API key's caveats. Holders can
// add caveats offline; each one can only narrow what the key allows.
type Restrictions struct {
	Projects []string  // nil = no limit
	MaxRole  Role      // "" = no limit
	Tasks    []string  // writes must fall under every listed task's subtree
	Actions  []Action  // nil = no limit
	Expires  time.Time // zero = no limit
	Label    string    // sub-agent label; nested attenuations join with "/"
}

// IsZero reports whether r carries no limits at all.
func (r Restrictions) IsZero() bool {
	return r.Projects == nil && r.MaxRole == "" && r.Tasks == nil && r.Actions == nil &&
		r.Expires.IsZero() && r.Label == ""
}

// rootKey derives an API key's macaroon root secret from the master secret,
// so no per-key secret is stored.
func rootKey(master []byte, keyID string) []byte {
	m := hmac.New(sha256.New, master)
	m.Write([]byte("tt macaroon root " + keyID))
	return m.Sum(nil)
}

// MintKey creates the root API key for keyID. Its limits live in the
// database record, so the root key carries no caveats.
func MintKey(master []byte, keyID string) (string, error) {
	m, err := macaroon.New(rootKey(master, keyID), []byte(keyID), keyLocation, macaroon.V2)
	if err != nil {
		return "", err
	}
	return encodeKey(m)
}

// Attenuate returns a new key that can do at most what key can, further
// limited by r. It needs no secret and no call to the server.
func Attenuate(key string, r Restrictions) (string, error) {
	m, err := decodeKey(key)
	if err != nil {
		return "", err
	}
	var caveats []string
	if r.Projects != nil {
		caveats = append(caveats, "project = "+strings.Join(r.Projects, ","))
	}
	if r.MaxRole != "" {
		caveats = append(caveats, "role = "+string(r.MaxRole))
	}
	for _, t := range r.Tasks {
		caveats = append(caveats, "task = "+t)
	}
	if r.Actions != nil {
		s := make([]string, len(r.Actions))
		for i, a := range r.Actions {
			s[i] = string(a)
		}
		caveats = append(caveats, "actions = "+strings.Join(s, ","))
	}
	if !r.Expires.IsZero() {
		caveats = append(caveats, "expires = "+r.Expires.UTC().Format(time.RFC3339))
	}
	if r.Label != "" {
		caveats = append(caveats, "label = "+r.Label)
	}
	if len(caveats) == 0 {
		return "", errors.New("no restrictions given")
	}
	for _, c := range caveats {
		if err := m.AddFirstPartyCaveat([]byte(c)); err != nil {
			return "", err
		}
	}
	return encodeKey(m)
}

// KeyID returns the ID of the root key a key was derived from, without
// verifying it.
func KeyID(key string) (string, error) {
	m, err := decodeKey(key)
	if err != nil {
		return "", err
	}
	return string(m.Id()), nil
}

// VerifyKey checks key's signature chain and returns its root key ID and the
// combined restrictions of all its caveats. Unknown caveats fail closed.
func VerifyKey(master []byte, key string, now time.Time) (string, Restrictions, error) {
	var r Restrictions
	m, err := decodeKey(key)
	if err != nil {
		return "", r, err
	}
	keyID := string(m.Id())
	conds, err := m.VerifySignature(rootKey(master, keyID), nil)
	if err != nil {
		return "", r, fmt.Errorf("invalid api key: %w", err)
	}
	for _, c := range conds {
		name, val, ok := strings.Cut(c, " = ")
		if !ok {
			return "", r, fmt.Errorf("malformed caveat %q", c)
		}
		switch name {
		case "project":
			r.Projects = intersect(r.Projects, strings.Split(val, ","))
		case "role":
			role, err := ParseRole(val)
			if err != nil {
				return "", r, err
			}
			r.MaxRole = MinRole(r.MaxRole, role)
		case "task":
			r.Tasks = append(r.Tasks, val)
		case "actions":
			var acts []Action
			for _, s := range strings.Split(val, ",") {
				a, err := ParseAction(s)
				if err != nil {
					return "", r, err
				}
				acts = append(acts, a)
			}
			r.Actions = intersect(r.Actions, acts)
		case "expires":
			t, err := time.Parse(time.RFC3339, val)
			if err != nil {
				return "", r, fmt.Errorf("bad expires caveat: %w", err)
			}
			if r.Expires.IsZero() || t.Before(r.Expires) {
				r.Expires = t
			}
		case "label":
			if r.Label == "" {
				r.Label = val
			} else {
				r.Label += "/" + val
			}
		default:
			return "", r, fmt.Errorf("unknown caveat %q", name)
		}
	}
	// An empty list would be dropped from the JWT by omitempty and read as
	// "no limit", so reject keys whose caveats leave nothing allowed.
	if r.Projects != nil && len(r.Projects) == 0 {
		return "", r, errors.New("api key allows no projects")
	}
	if r.Actions != nil && len(r.Actions) == 0 {
		return "", r, errors.New("api key allows no actions")
	}
	if !r.Expires.IsZero() && !now.Before(r.Expires) {
		return "", r, errors.New("api key expired")
	}
	return keyID, r, nil
}

// intersect narrows cur (nil = everything) to the values also in next.
func intersect[T comparable](cur, next []T) []T {
	if cur == nil {
		return slices.Clone(next)
	}
	out := []T{}
	for _, v := range cur {
		if slices.Contains(next, v) {
			out = append(out, v)
		}
	}
	return out
}

func encodeKey(m *macaroon.Macaroon) (string, error) {
	b, err := m.MarshalBinary()
	if err != nil {
		return "", err
	}
	return KeyPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func decodeKey(key string) (*macaroon.Macaroon, error) {
	raw, ok := strings.CutPrefix(key, KeyPrefix)
	if !ok {
		return nil, errors.New("not a tt api key")
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode api key: %w", err)
	}
	var m macaroon.Macaroon
	if err := m.UnmarshalBinary(b); err != nil {
		return nil, fmt.Errorf("decode api key: %w", err)
	}
	return &m, nil
}
