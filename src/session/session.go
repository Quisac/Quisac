package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// Signer produces and checks signed values using a secret key.
type Signer struct {
	key []byte
}

// NewSigner returns a Signer keyed by secret (the QUISAC_SESSION_SECRET).
func NewSigner(secret string) *Signer {
	return &Signer{key: []byte(secret)}
}

// Sign returns value with an appended signature: "<value>.<sig>".
func (s *Signer) Sign(value string) string {
	return value + "." + s.mac(value)
}

// Verify splits a signed token, recomputes the signature, and returns the
// original value when it matches. The boolean is false for any tampered,
// malformed, or empty token.
func (s *Signer) Verify(token string) (string, bool) {
	value, sig, ok := strings.Cut(token, ".")
	if !ok {
		return "", false
	}
	if !hmac.Equal([]byte(sig), []byte(s.mac(value))) {
		return "", false
	}
	return value, true
}

// mac returns the base64url HMAC-SHA256 of value under the signer's key.
func (s *Signer) mac(value string) string {
	h := hmac.New(sha256.New, s.key)
	h.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
