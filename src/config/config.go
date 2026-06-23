// Package config loads runtime configuration from the environment.
//
// In local development the values come from a .env file in the working
// directory; in production (Railway) they are injected as real environment
// variables. Either way the rest of the app reads them through a single
// typed Config struct.
//
// The struct layout and SetValuesFromEnv pattern mirror Palantir's
// go-githubapp config, adapted to this project's existing env var names.
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is the fully resolved configuration for the server.
type Config struct {
	GitHub GitHub
	Server Server
}

// GitHub holds everything needed to authenticate as the GitHub App and to
// drive the install + authorize flow.
type GitHub struct {
	// AppSlug is the app's URL slug, used to build the installation URL
	// github.com/apps/<slug>/installations/new.
	AppSlug string
	// RedirectURL is where GitHub sends the user after install + authorize.
	RedirectURL string

	App struct {
		IntegrationID int64  // the App ID
		WebhookSecret string // HMAC secret for webhook verification
		PrivateKey    string // PEM private key for signing the app JWT
	}

	OAuth struct {
		ClientID     string
		ClientSecret string
	}
}

// Server holds HTTP server settings.
type Server struct {
	Port          string
	SessionSecret string // signs session cookies
}

// Load reads .env (if present) into the process environment, populates a
// Config from environment variables, and validates that required values are
// present so the server fails fast instead of half-configured.
func Load() (*Config, error) {
	loadDotEnv(".env")

	c := &Config{}
	c.Server.Port = "8000"
	c.SetValuesFromEnv()

	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// SetValuesFromEnv sets fields from their corresponding environment variables
// when those variables are present, leaving existing values untouched
// otherwise.
func (c *Config) SetValuesFromEnv() {
	setStringFromEnv("GITHUB_APP_SLUG", &c.GitHub.AppSlug)
	setStringFromEnv("GITHUB_REDIRECT_URL", &c.GitHub.RedirectURL)

	setIntFromEnv("GITHUB_APP_ID", &c.GitHub.App.IntegrationID)
	setStringFromEnv("GITHUB_WEBHOOK_SECRET", &c.GitHub.App.WebhookSecret)
	setStringFromEnv("GITHUB_APP_PRIVATE_KEY", &c.GitHub.App.PrivateKey)

	setStringFromEnv("GITHUB_CLIENT_ID", &c.GitHub.OAuth.ClientID)
	setStringFromEnv("GITHUB_CLIENT_SECRET", &c.GitHub.OAuth.ClientSecret)

	setStringFromEnv("QUISAC_SESSION_SECRET", &c.Server.SessionSecret)
	setStringFromEnv("PORT", &c.Server.Port)
}

// validate returns an error naming the first required value that is missing.
func (c *Config) validate() error {
	missing := []struct {
		name string
		set  bool
	}{
		{"GITHUB_APP_SLUG", c.GitHub.AppSlug != ""},
		{"GITHUB_APP_ID", c.GitHub.App.IntegrationID != 0},
		{"GITHUB_REDIRECT_URL", c.GitHub.RedirectURL != ""},
		{"GITHUB_CLIENT_ID", c.GitHub.OAuth.ClientID != ""},
		{"GITHUB_CLIENT_SECRET", c.GitHub.OAuth.ClientSecret != ""},
		{"QUISAC_SESSION_SECRET", c.Server.SessionSecret != ""},
	}
	for _, m := range missing {
		if !m.set {
			return fmt.Errorf("config: missing required env var %s", m.name)
		}
	}
	return nil
}

func setStringFromEnv(key string, value *string) {
	if v, ok := os.LookupEnv(key); ok {
		*value = v
	}
}

func setIntFromEnv(key string, value *int64) {
	if v, ok := os.LookupEnv(key); ok {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			*value = i
		}
	}
}

// loadDotEnv reads KEY=VALUE lines from path into the environment without
// overwriting variables that are already set (real env wins over .env).
// A missing file is not an error — production has no .env.
//
// Double-quoted values may span multiple lines, which is how a PEM private
// key is stored in .env. Such a value runs until the closing quote.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		trimmed := strings.TrimSpace(scanner.Text())
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, val, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)

		// A value that opens with a quote but isn't closed on the same line
		// continues onto following lines (e.g. a multi-line PEM key).
		if strings.HasPrefix(val, `"`) && !isClosedQuote(val) {
			var b strings.Builder
			b.WriteString(strings.TrimPrefix(val, `"`))
			for scanner.Scan() {
				next := scanner.Text()
				if i := strings.IndexByte(next, '"'); i >= 0 {
					b.WriteByte('\n')
					b.WriteString(next[:i])
					break
				}
				b.WriteByte('\n')
				b.WriteString(next)
			}
			val = b.String()
		} else {
			val = strings.Trim(strings.TrimSpace(val), `"`)
		}

		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
		}
	}
}

// isClosedQuote reports whether a value that starts with a double quote also
// has a matching closing quote on the same line.
func isClosedQuote(val string) bool {
	return len(val) >= 2 && strings.HasSuffix(val, `"`)
}
