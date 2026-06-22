package github

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds GitHub App OAuth credentials.
type Config struct {
	AppSlug       string
	ClientID      string
	ClientSecret  string
	RedirectURL   string
	SessionSecret string
	AppID         int64
	PrivateKeyPEM string
	APIBaseURL    string
	WebBaseURL    string
}

const (
	defaultAPIBaseURL = "https://api.github.com"
	defaultWebBaseURL = "https://github.com"
)

func (c Config) apiBaseURL() string {
	if c.APIBaseURL == "" {
		return defaultAPIBaseURL
	}
	return strings.TrimSuffix(c.APIBaseURL, "/")
}

func (c Config) webBaseURL() string {
	if c.WebBaseURL == "" {
		return defaultWebBaseURL
	}
	return strings.TrimSuffix(c.WebBaseURL, "/")
}

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
		APIBaseURL:    os.Getenv("GITHUB_API_BASE_URL"),
		WebBaseURL:    os.Getenv("GITHUB_WEB_BASE_URL"),
	}
}

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
