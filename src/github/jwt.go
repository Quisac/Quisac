package github

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"time"
)

// appJWT mints a short-lived RS256 JSON Web Token signed with the app's
// private key. GitHub accepts this token as the app's identity when exchanging
// it for an installation access token.
//
// The token is valid for ten minutes (GitHub's maximum) and back-dated 60s to
// tolerate clock skew, per GitHub's guidance.
func (a *App) appJWT() (string, error) {
	key, err := parsePrivateKey(a.privateKey)
	if err != nil {
		return "", err
	}

	now := time.Now()
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": a.appID,
	}

	signingInput, err := encodeSegment(header)
	if err != nil {
		return "", err
	}
	claimsSegment, err := encodeSegment(claims)
	if err != nil {
		return "", err
	}
	signingInput += "." + claimsSegment

	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// encodeSegment JSON-encodes v and returns its base64url (no padding) form.
func encodeSegment(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// parsePrivateKey decodes a PEM-encoded RSA private key in either PKCS#1
// ("RSA PRIVATE KEY") or PKCS#8 ("PRIVATE KEY") form.
func parsePrivateKey(pemKey string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemKey))
	if block == nil {
		return nil, fmt.Errorf("github: private key is not valid PEM")
	}

	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("github: cannot parse private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("github: private key is not RSA")
	}
	return key, nil
}
