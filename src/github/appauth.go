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

const appJWTLifetime = 9 * time.Minute

const appJWTClockSkew = 60 * time.Second

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
