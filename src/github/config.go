package github

import (
	"fmt"
	"os"
)

// Config holds GitHub App OAuth credentials.
type Config struct {
	AppSlug      string
	ClientID     string
	ClientSecret string
	RedirectURL  string

	// SessionSecret signs the session cookie (HMAC-SHA256). Required.
	SessionSecret string
}

// ConfigFromEnv loads Config from the standard environment variables.
func ConfigFromEnv() Config {
	return Config{
		AppSlug:       os.Getenv("GITHUB_APP_SLUG"),
		ClientID:      os.Getenv("GITHUB_CLIENT_ID"),
		ClientSecret:  os.Getenv("GITHUB_CLIENT_SECRET"),
		RedirectURL:   os.Getenv("GITHUB_REDIRECT_URL"),
		SessionSecret: os.Getenv("QUISAC_SESSION_SECRET"),
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
