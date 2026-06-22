package github

import (
	"os"
	"testing"
)

func TestConfigFromEnv(t *testing.T) {
	tests := map[string]struct {
		Variables map[string]string
		Output    Config
	}{
		"allVariables": {
			Variables: map[string]string{
				"GITHUB_APP_SLUG":       "my-app",
				"GITHUB_CLIENT_ID":      "client-id-123",
				"GITHUB_CLIENT_SECRET":  "client-secret-abc",
				"GITHUB_REDIRECT_URL":   "https://example.com/callback",
				"QUISAC_SESSION_SECRET": "session-secret-xyz",
				"GITHUB_APP_ID":         "42",
				"GITHUB_APP_PRIVATE_KEY": "-----BEGIN RSA PRIVATE KEY-----\nxxx\n-----END RSA PRIVATE KEY-----",
				"GITHUB_API_BASE_URL":   "https://api.github.example.com",
				"GITHUB_WEB_BASE_URL":   "https://github.example.com",
			},
			Output: Config{
				AppSlug:       "my-app",
				ClientID:      "client-id-123",
				ClientSecret:  "client-secret-abc",
				RedirectURL:   "https://example.com/callback",
				SessionSecret: "session-secret-xyz",
				AppID:         42,
				PrivateKeyPEM: "-----BEGIN RSA PRIVATE KEY-----\nxxx\n-----END RSA PRIVATE KEY-----",
				APIBaseURL:    "https://api.github.example.com",
				WebBaseURL:    "https://github.example.com",
			},
		},
		"noVariables": {
			Variables: map[string]string{},
			Output:    Config{},
		},
		"partialVariables": {
			Variables: map[string]string{
				"GITHUB_APP_SLUG":  "partial-app",
				"GITHUB_CLIENT_ID": "partial-client",
			},
			Output: Config{
				AppSlug:  "partial-app",
				ClientID: "partial-client",
			},
		},
		"invalidAppID": {
			Variables: map[string]string{
				"GITHUB_APP_ID": "not-a-number",
			},
			Output: Config{
				AppID: 0,
			},
		},
		"zeroAppID": {
			Variables: map[string]string{
				"GITHUB_APP_ID": "0",
			},
			Output: Config{
				AppID: 0,
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			// Clear all relevant env vars before each test
			allKeys := []string{
				"GITHUB_APP_SLUG", "GITHUB_CLIENT_ID", "GITHUB_CLIENT_SECRET",
				"GITHUB_REDIRECT_URL", "QUISAC_SESSION_SECRET", "GITHUB_APP_ID",
				"GITHUB_APP_PRIVATE_KEY", "GITHUB_API_BASE_URL", "GITHUB_WEB_BASE_URL",
			}
			for _, k := range allKeys {
				os.Unsetenv(k)
			}

			for k, v := range test.Variables {
				if err := os.Setenv(k, v); err != nil {
					t.Fatalf("failed to set environment variable %s: %v", k, err)
				}
			}
			defer func() {
				for k := range test.Variables {
					if err := os.Unsetenv(k); err != nil {
						t.Fatalf("failed to unset environment variable %s: %v", k, err)
					}
				}
			}()

			got := ConfigFromEnv()

			if got != test.Output {
				t.Errorf("incorrect config\nexpected: %+v\n  actual: %+v", test.Output, got)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	tests := map[string]struct {
		Input   Config
		WantErr bool
	}{
		"allFieldsPresent": {
			Input: Config{
				AppSlug:       "my-app",
				ClientID:      "client-id",
				ClientSecret:  "client-secret",
				RedirectURL:   "https://example.com/callback",
				SessionSecret: "session-secret",
			},
			WantErr: false,
		},
		"missingAppSlug": {
			Input: Config{
				ClientID:      "client-id",
				ClientSecret:  "client-secret",
				RedirectURL:   "https://example.com/callback",
				SessionSecret: "session-secret",
			},
			WantErr: true,
		},
		"missingClientID": {
			Input: Config{
				AppSlug:       "my-app",
				ClientSecret:  "client-secret",
				RedirectURL:   "https://example.com/callback",
				SessionSecret: "session-secret",
			},
			WantErr: true,
		},
		"missingClientSecret": {
			Input: Config{
				AppSlug:       "my-app",
				ClientID:      "client-id",
				RedirectURL:   "https://example.com/callback",
				SessionSecret: "session-secret",
			},
			WantErr: true,
		},
		"missingRedirectURL": {
			Input: Config{
				AppSlug:       "my-app",
				ClientID:      "client-id",
				ClientSecret:  "client-secret",
				SessionSecret: "session-secret",
			},
			WantErr: true,
		},
		"missingSessionSecret": {
			Input: Config{
				AppSlug:      "my-app",
				ClientID:     "client-id",
				ClientSecret: "client-secret",
				RedirectURL:  "https://example.com/callback",
			},
			WantErr: true,
		},
		"allFieldsMissing": {
			Input:   Config{},
			WantErr: true,
		},
		"extraFieldsDoNotAffectValidation": {
			Input: Config{
				AppSlug:       "my-app",
				ClientID:      "client-id",
				ClientSecret:  "client-secret",
				RedirectURL:   "https://example.com/callback",
				SessionSecret: "session-secret",
				AppID:         99,
				PrivateKeyPEM: "some-key",
				APIBaseURL:    "https://api.github.example.com",
				WebBaseURL:    "https://github.example.com",
			},
			WantErr: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			err := test.Input.Validate()
			if (err != nil) != test.WantErr {
				t.Errorf("Validate() error = %v, wantErr = %v", err, test.WantErr)
			}
		})
	}
}

func TestConfigValidateAppAuth(t *testing.T) {
	tests := map[string]struct {
		Input   Config
		WantErr bool
	}{
		"bothPresent": {
			Input: Config{
				AppID:         42,
				PrivateKeyPEM: "-----BEGIN RSA PRIVATE KEY-----\nxxx\n-----END RSA PRIVATE KEY-----",
			},
			WantErr: false,
		},
		"missingAppID": {
			Input: Config{
				PrivateKeyPEM: "-----BEGIN RSA PRIVATE KEY-----\nxxx\n-----END RSA PRIVATE KEY-----",
			},
			WantErr: true,
		},
		"missingPrivateKey": {
			Input: Config{
				AppID: 42,
			},
			WantErr: true,
		},
		"bothMissing": {
			Input:   Config{},
			WantErr: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			err := test.Input.ValidateAppAuth()
			if (err != nil) != test.WantErr {
				t.Errorf("ValidateAppAuth() error = %v, wantErr = %v", err, test.WantErr)
			}
		})
	}
}

func TestConfigAPIBaseURL(t *testing.T) {
	tests := map[string]struct {
		Input  Config
		Output string
	}{
		"empty uses default": {
			Input:  Config{},
			Output: defaultAPIBaseURL,
		},
		"custom URL": {
			Input:  Config{APIBaseURL: "https://api.github.example.com"},
			Output: "https://api.github.example.com",
		},
		"trailing slash is trimmed": {
			Input:  Config{APIBaseURL: "https://api.github.example.com/"},
			Output: "https://api.github.example.com",
		},
		"multiple trailing slashes trimmed": {
			Input:  Config{APIBaseURL: "https://api.github.example.com///"},
			Output: "https://api.github.example.com//",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got := test.Input.apiBaseURL()
			if got != test.Output {
				t.Errorf("apiBaseURL() = %q, want %q", got, test.Output)
			}
		})
	}
}

func TestConfigWebBaseURL(t *testing.T) {
	tests := map[string]struct {
		Input  Config
		Output string
	}{
		"empty uses default": {
			Input:  Config{},
			Output: defaultWebBaseURL,
		},
		"custom URL": {
			Input:  Config{WebBaseURL: "https://github.example.com"},
			Output: "https://github.example.com",
		},
		"trailing slash is trimmed": {
			Input:  Config{WebBaseURL: "https://github.example.com/"},
			Output: "https://github.example.com",
		},
		"multiple trailing slashes trimmed": {
			Input:  Config{WebBaseURL: "https://github.example.com///"},
			Output: "https://github.example.com//",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got := test.Input.webBaseURL()
			if got != test.Output {
				t.Errorf("webBaseURL() = %q, want %q", got, test.Output)
			}
		})
	}
}
