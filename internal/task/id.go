package task

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
)

var encoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

func NewID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return encoding.EncodeToString(b)[:4]
}

func ShortID(id string) string {
	if len(id) > 6 {
		return id[:6]
	}
	return id
}

// PrefixMatch returns true if id starts with prefix (case-insensitive).
func PrefixMatch(id, prefix string) bool {
	return strings.HasPrefix(strings.ToLower(id), strings.ToLower(prefix))
}
