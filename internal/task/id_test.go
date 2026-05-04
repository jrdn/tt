package task

import (
	"testing"
)

func TestNewID_Length(t *testing.T) {
	id := NewID()
	if len(id) != 4 {
		t.Errorf("expected 4-char ID, got %q (len %d)", id, len(id))
	}
}

func TestNewID_Unique(t *testing.T) {
	// 4-char base32 = ~1M values; 50 samples gives <0.12% collision probability
	seen := make(map[string]bool)
	for range 50 {
		id := NewID()
		if seen[id] {
			t.Fatalf("collision on ID %q after %d generations", id, len(seen))
		}
		seen[id] = true
	}
}

func TestNewID_Charset(t *testing.T) {
	const valid = "abcdefghijklmnopqrstuvwxyz234567"
	for range 100 {
		id := NewID()
		for _, c := range id {
			if !contains(valid, byte(c)) {
				t.Errorf("ID %q contains invalid character %q", id, c)
			}
		}
	}
}

func TestPrefixMatch(t *testing.T) {
	cases := []struct {
		id, prefix string
		want       bool
	}{
		{"abcd", "ab", true},
		{"abcd", "abcd", true},
		{"abcd", "abcde", false},
		{"abcd", "xyz", false},
		{"ABCD", "ab", true},
		{"abcd", "AB", true},
	}
	for _, c := range cases {
		if got := PrefixMatch(c.id, c.prefix); got != c.want {
			t.Errorf("PrefixMatch(%q, %q) = %v, want %v", c.id, c.prefix, got, c.want)
		}
	}
}

func contains(s string, b byte) bool {
	for i := range len(s) {
		if s[i] == b {
			return true
		}
	}
	return false
}
