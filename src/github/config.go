package github

import (
	"fmt"
	"os"
)

type Config struct {
	AppSlug      string
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

func LoadConfig() (Config, error) {
	cfg := Config{
		AppSlug:      os.Getenv("GITHUB_APP_SLUG"),
		ClientID:     os.Getenv("GITHUB_CLIENT_ID"),
		ClientSecret: os.Getenv("GITHUB_CLIENT_SECRET"),
		RedirectURL:  os.Getenv("GITHUB_REDIRECT_URL"),
	}

	if cfg.AppSlug == "" ||
		cfg.ClientID == "" ||
		cfg.ClientSecret == "" ||
		cfg.RedirectURL == "" {
		return Config{}, fmt.Errorf("missing github environment variables")
	}

	return cfg, nil
}
