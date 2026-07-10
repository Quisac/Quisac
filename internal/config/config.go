package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	GitHub   GitHub
	Server   Server
	Database Database
	Neo4j    Neo4j
}

type Database struct {
	URL string // DATABASE_URL, e.g. postgres://user:pass@hc
}

// Neo4j holds the connection settings for the code-graph store. For a local
// Neo4j Desktop instance the URI is typically neo4j://localhost:7687.
type Neo4j struct {
	URI      string // NEO4J_URI
	User     string // NEO4J_USER
	Password string // NEO4J_PASSWORD
}

type GitHub struct {
	AppSlug     string
	RedirectURL string

	App struct {
		IntegrationID int64
		WebhookSecret string
		PrivateKey    string
	}

	OAuth struct {
		ClientID     string
		ClientSecret string
	}
}

type Server struct {
	Port          string
	SessionSecret string
}

func Load() (*Config, error) {
	_ = godotenv.Load() // no-op if .env is absent

	c := &Config{}
	c.Server.Port = "8000"
	c.SetValuesFromEnv()

	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

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
	setStringFromEnv("DATABASE_URL", &c.Database.URL)
	setStringFromEnv("NEO4J_URI", &c.Neo4j.URI)
	setStringFromEnv("NEO4J_USER", &c.Neo4j.User)
	setStringFromEnv("NEO4J_PASSWORD", &c.Neo4j.Password)
}

func (c *Config) validate() error {
	required := []struct {
		name string
		set  bool
	}{
		{"GITHUB_APP_SLUG", c.GitHub.AppSlug != ""},
		{"GITHUB_APP_ID", c.GitHub.App.IntegrationID != 0},
		{"GITHUB_REDIRECT_URL", c.GitHub.RedirectURL != ""},
		{"GITHUB_CLIENT_ID", c.GitHub.OAuth.ClientID != ""},
		{"GITHUB_CLIENT_SECRET", c.GitHub.OAuth.ClientSecret != ""},
		{"GITHUB_WEBHOOK_SECRET", c.GitHub.App.WebhookSecret != ""},
		{"QUISAC_SESSION_SECRET", c.Server.SessionSecret != ""},
		{"DATABASE_URL", c.Database.URL != ""},
		{"NEO4J_URI", c.Neo4j.URI != ""},
		{"NEO4J_USER", c.Neo4j.User != ""},
		{"NEO4J_PASSWORD", c.Neo4j.Password != ""},
	}
	for _, r := range required {
		if !r.set {
			return fmt.Errorf("config: missing required env var %s", r.name)
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
