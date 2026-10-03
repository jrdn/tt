package task

import (
	"crypto/rand"
	"strings"
)

const idAlphabet = "0123456789abcdefghjkmnpqrstvwxyz"
const idLen = 7

func NewID() string {
	b := make([]byte, idLen)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i, v := range b {
		b[i] = idAlphabet[v%byte(len(idAlphabet))]
	}
	return string(b)
}

func ShortID(id string) string {
	if len(id) > idLen {
		return id[:idLen]
	}
	return id
}

// PrefixMatch returns true if id starts with prefix (case-insensitive).
func PrefixMatch(id, prefix string) bool {
	return strings.HasPrefix(strings.ToLower(id), strings.ToLower(prefix))
}
