package github

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds GitHub App OAuth credentials.
type Config struct {
	AppSlug      string
	ClientID     string
	ClientSecret string
	RedirectURL  string

	// SessionSecret signs the session cookie (HMAC-SHA256). Required.
	SessionSecret string

	// AppID is the GitHub App's numeric ID (shown as "App ID" at the top
	// of the app's settings page — distinct from AppSlug, which is the
	// URL-slug name). Required only for app-level JWT auth (installation
	// token minting), not for the OAuth install flow.
	AppID int64

	// PrivateKeyPEM is the full PEM-encoded contents of the GitHub App's
	// private key (PKCS1 or PKCS8), including the BEGIN/END lines.
	// Required only for app-level JWT auth.
	PrivateKeyPEM string

	// APIBaseURL is the GitHub REST API host, e.g. "https://api.github.com"
	// for github.com, or "https://github.example.com/api/v3" for a GitHub
	// Enterprise Server instance. Defaults to github.com if empty — see
	// apiBaseURL(). Configurable so tests can point it at a fake server
	// and so a future GHE deployment doesn't require code changes.
	APIBaseURL string

	// WebBaseURL is the GitHub web host used for the OAuth/installation
	// browser flow, e.g. "https://github.com". Defaults to github.com if
	// empty — see webBaseURL().
	WebBaseURL string
}

// defaultAPIBaseURL and defaultWebBaseURL are github.com's hosts, used
// when Config doesn't set APIBaseURL/WebBaseURL explicitly.
const (
	defaultAPIBaseURL = "https://api.github.com"
	defaultWebBaseURL = "https://github.com"
)

// apiBaseURL returns c.APIBaseURL with the github.com default applied, and
// any trailing slash trimmed so callers can safely do apiBaseURL()+"/path".
func (c Config) apiBaseURL() string {
	if c.APIBaseURL == "" {
		return defaultAPIBaseURL
	}
	return strings.TrimSuffix(c.APIBaseURL, "/")
}

// webBaseURL returns c.WebBaseURL with the github.com default applied, and
// any trailing slash trimmed.
func (c Config) webBaseURL() string {
	if c.WebBaseURL == "" {
		return defaultWebBaseURL
	}
	return strings.TrimSuffix(c.WebBaseURL, "/")
}

// ConfigFromEnv loads Config from the standard environment variables.
func ConfigFromEnv() Config {
	appID, _ := strconv.ParseInt(os.Getenv("GITHUB_APP_ID"), 10, 64)
	return Config{
		AppSlug:       os.Getenv("GITHUB_APP_SLUG"),
		ClientID:      os.Getenv("GITHUB_CLIENT_ID"),
		ClientSecret:  os.Getenv("GITHUB_CLIENT_SECRET"),
		RedirectURL:   os.Getenv("GITHUB_REDIRECT_URL"),
		SessionSecret: os.Getenv("QUISAC_SESSION_SECRET"),
		AppID:         appID,
		PrivateKeyPEM: os.Getenv("GITHUB_APP_PRIVATE_KEY"),
		// Both optional — empty means "use github.com", see apiBaseURL/webBaseURL.
		APIBaseURL: os.Getenv("GITHUB_API_BASE_URL"),
		WebBaseURL: os.Getenv("GITHUB_WEB_BASE_URL"),
	}
}

// Validate returns an error if any required field is missing.
func (c Config) Validate() error {
	required := map[string]string{
		"GITHUB_APP_SLUG":       c.AppSlug,
		"GITHUB_CLIENT_ID":      c.ClientID,
		"GITHUB_CLIENT_SECRET":  c.ClientSecret,
		"GITHUB_REDIRECT_URL":   c.RedirectURL,
		"QUISAC_SESSION_SECRET": c.SessionSecret,
	}

	var missing []string
	for name, val := range required {
		if val == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("github: missing env vars: %v", missing)
	}
	return nil
}

// ValidateAppAuth returns an error if config needed for app-level JWT auth
// (installation token minting) is missing. This is checked separately from
// Validate so the OAuth install flow can run without app-JWT credentials
// configured; the error surfaces at first use of installation auth instead
// of at server startup.
func (c Config) ValidateAppAuth() error {
	var missing []string
	if c.AppID == 0 {
		missing = append(missing, "GITHUB_APP_ID")
	}
	if c.PrivateKeyPEM == "" {
		missing = append(missing, "GITHUB_APP_PRIVATE_KEY")
	}
	if len(missing) > 0 {
		return fmt.Errorf("github: missing env vars for app auth: %v", missing)
	}
	return nil
}
