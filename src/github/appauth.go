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

// appJWTLifetime is how long a generated app JWT is valid for. GitHub
// allows a maximum of 10 minutes; this stays comfortably under that to
// tolerate clock drift between this server and GitHub's.
const appJWTLifetime = 9 * time.Minute

// appJWTClockSkew is subtracted from "now" when setting the JWT's issued-at
// time, since GitHub rejects tokens issued in the future if its clock is
// even slightly ahead of this server's.
const appJWTClockSkew = 60 * time.Second

// parsePrivateKey parses a PEM-encoded RSA private key in either PKCS1 or
// PKCS8 form, matching what GitHub Apps generate when you download a
// private key from the app settings page.
func parsePrivateKey(pemData string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, fmt.Errorf("github: no PEM block found in private key")
	}

	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("github: parse private key (tried PKCS1 and PKCS8): %w", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("github: private key is not an RSA key")
	}
	return rsaKey, nil
}

// signAppJWT builds and signs a JWT asserting identity as the GitHub App
// with the given ID, per GitHub's app-authentication scheme:
// https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-json-web-token-jwt-for-a-github-app
//
// This is a minimal, dependency-free RS256 JWT implementation — GitHub App
// auth needs exactly this one token shape, so a general-purpose JWT library
// isn't pulled in for it.
func signAppJWT(appID int64, key *rsa.PrivateKey, now time.Time) (string, error) {
	header := map[string]string{"alg": "RS256", "typ": "JWT"}

	claims := map[string]any{
		"iat": now.Add(-appJWTClockSkew).Unix(),
		"exp": now.Add(appJWTLifetime).Unix(),
		"iss": fmt.Sprintf("%d", appID),
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("github: marshal jwt header: %w", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("github: marshal jwt claims: %w", err)
	}

	signingInput := base64URLEncode(headerJSON) + "." + base64URLEncode(claimsJSON)

	sig, err := signRS256(key, signingInput)
	if err != nil {
		return "", fmt.Errorf("github: sign jwt: %w", err)
	}

	return signingInput + "." + base64URLEncode(sig), nil
}

func signRS256(key *rsa.PrivateKey, signingInput string) ([]byte, error) {
	hashed := sha256.Sum256([]byte(signingInput))
	return rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hashed[:])
}

func base64URLEncode(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}
