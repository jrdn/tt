package auth

import (
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func init() {
	// Millisecond iat/exp so revocation cutoffs (revoked_before) aren't
	// ambiguous within a second. Fractional NumericDates are valid JWT.
	jwt.TimePrecision = time.Millisecond
}

// Issuer signs and verifies tt JWTs. Its Ed25519 key is derived from the
// server's master secret, so nothing secret is stored in the database;
// rotating means bumping the key version.
type Issuer struct {
	name string
	ttl  time.Duration
	kid  string
	priv ed25519.PrivateKey
	pubs map[string]ed25519.PublicKey // kid -> key, including retired versions
	now  func() time.Time
}

// NewIssuer derives signing keys for versions 1..version from master and
// signs with the newest. Older versions still verify until their tokens
// expire.
func NewIssuer(master []byte, name string, ttl time.Duration, version int) (*Issuer, error) {
	if len(master) < 32 {
		return nil, errors.New("master key must be at least 32 bytes")
	}
	if version < 1 {
		return nil, errors.New("key version must be >= 1")
	}
	iss := &Issuer{name: name, ttl: ttl, pubs: map[string]ed25519.PublicKey{}, now: time.Now}
	for v := 1; v <= version; v++ {
		kid := fmt.Sprintf("v%d", v)
		seed, err := hkdf.Key(sha256.New, master, nil, "tt jwt signing "+kid, ed25519.SeedSize)
		if err != nil {
			return nil, err
		}
		priv := ed25519.NewKeyFromSeed(seed)
		iss.pubs[kid] = priv.Public().(ed25519.PublicKey)
		iss.kid, iss.priv = kid, priv
	}
	return iss, nil
}

// Sign fills in issuer, timing and ID fields and returns a signed token.
// A non-zero notAfter caps the expiry (e.g. at an API key's expiry).
func (i *Issuer) Sign(c *Claims, notAfter time.Time) (string, error) {
	now := i.now()
	exp := now.Add(i.ttl)
	if !notAfter.IsZero() && notAfter.Before(exp) {
		exp = notAfter
	}
	c.Issuer = i.name
	c.IssuedAt = jwt.NewNumericDate(now)
	c.ExpiresAt = jwt.NewNumericDate(exp)
	c.ID = randomID()
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, c)
	tok.Header["kid"] = i.kid
	return tok.SignedString(i.priv)
}

// Verify checks the signature, issuer and expiry and returns the claims.
func (i *Issuer) Verify(token string) (*Claims, error) {
	c := &Claims{}
	_, err := jwt.ParseWithClaims(token, c, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		pub, ok := i.pubs[kid]
		if !ok {
			return nil, fmt.Errorf("unknown key id %q", kid)
		}
		return pub, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithIssuer(i.name),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(i.now),
	)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// JWKS returns the public keys in JSON Web Key Set form, for
// /.well-known/jwks.json.
func (i *Issuer) JWKS() map[string]any {
	keys := []map[string]string{}
	for kid, pub := range i.pubs {
		keys = append(keys, map[string]string{
			"kty": "OKP",
			"crv": "Ed25519",
			"alg": "EdDSA",
			"use": "sig",
			"kid": kid,
			"x":   base64.RawURLEncoding.EncodeToString(pub),
		})
	}
	return map[string]any{"keys": keys}
}

func randomID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
