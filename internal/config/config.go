package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	GitHub     GitHub
	Server     Server
	Database   Database
	Neo4j      Neo4j
	LLM        LLM
	Resolution Resolution
}

// LLM configures the model that proposes fixes for incidents the pattern cache
// has not seen. It is entirely optional: with no API key the pipeline still
// ingests alerts, traverses the graph, and replays known fixes — novel
// incidents simply stop at a ranked root-cause analysis instead of a patch.
type LLM struct {
	Provider string // QUISAC_LLM_PROVIDER — anthropic | gemini
	APIKey   string // ANTHROPIC_API_KEY or GEMINI_API_KEY, per provider
	Model    string // QUISAC_LLM_MODEL   — defaults to the provider's choice
	Effort   string // QUISAC_LLM_EFFORT  — low | medium | high | xhigh | max
}

// Resolution tunes the fix pipeline, including whether it is allowed to open
// pull requests on its own.
type Resolution struct {
	// AutoPR controls whether a validated fix is pushed and opened as a pull
	// request without a human in the loop. Defaults to true, paired with
	// DraftPR — the point of the product is that the fix is waiting for you.
	AutoPR bool // QUISAC_AUTO_PR

	// DraftPR opens generated pull requests as drafts. Defaults to true: a
	// machine-authored change to production code should land somewhere visible
	// and reviewable, but not somewhere it can be merged by reflex.
	DraftPR bool // QUISAC_DRAFT_PR

	BranchPrefix  string  // QUISAC_BRANCH_PREFIX  — branch namespace for fixes
	MinConfidence float64 // QUISAC_MIN_CONFIDENCE — below this, no PR is opened
	MaxFixFiles   int64   // QUISAC_MAX_FIX_FILES  — files ever sent to the model
	MaxDepth      int64   // QUISAC_MAX_DEPTH      — BFS hops back from the failure
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

	// PagerDutyWebhookSecret is the signing secret of the PagerDuty webhook
	// subscription. Unset, deliveries are accepted unverified — dev only.
	PagerDutyWebhookSecret string
}

func Load() (*Config, error) {
	_ = godotenv.Load() // no-op if .env is absent

	c := &Config{}
	c.Server.Port = "8000"
	c.Resolution.AutoPR = true
	c.Resolution.DraftPR = true
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
	setStringFromEnv("PAGERDUTY_WEBHOOK_SECRET", &c.Server.PagerDutyWebhookSecret)
	setStringFromEnv("PORT", &c.Server.Port)
	setStringFromEnv("DATABASE_URL", &c.Database.URL)
	setStringFromEnv("NEO4J_URI", &c.Neo4j.URI)
	setStringFromEnv("NEO4J_USER", &c.Neo4j.User)
	setStringFromEnv("NEO4J_PASSWORD", &c.Neo4j.Password)
	setStringFromEnv("QUISAC_LLM_PROVIDER", &c.LLM.Provider)
	setStringFromEnv("QUISAC_LLM_MODEL", &c.LLM.Model)
	setStringFromEnv("QUISAC_LLM_EFFORT", &c.LLM.Effort)
	resolveLLMKey(&c.LLM)
	setBoolFromEnv("QUISAC_AUTO_PR", &c.Resolution.AutoPR)
	setBoolFromEnv("QUISAC_DRAFT_PR", &c.Resolution.DraftPR)
	setStringFromEnv("QUISAC_BRANCH_PREFIX", &c.Resolution.BranchPrefix)
	setFloatFromEnv("QUISAC_MIN_CONFIDENCE", &c.Resolution.MinConfidence)
	setIntFromEnv("QUISAC_MAX_FIX_FILES", &c.Resolution.MaxFixFiles)
	setIntFromEnv("QUISAC_MAX_DEPTH", &c.Resolution.MaxDepth)
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

// resolveLLMKey picks the API key for the configured provider. Each vendor keeps
// its own conventional variable name, so a machine can hold keys for both and
// QUISAC_LLM_PROVIDER alone decides which one is used.
//
// When no provider is set, whichever key is present selects it — setting only
// GEMINI_API_KEY is an unambiguous statement of intent, and making the operator
// also name the provider would buy nothing.
func resolveLLMKey(l *LLM) {
	var anthropicKey, geminiKey string
	setStringFromEnv("ANTHROPIC_API_KEY", &anthropicKey)
	setStringFromEnv("GEMINI_API_KEY", &geminiKey)

	switch strings.ToLower(strings.TrimSpace(l.Provider)) {
	case "anthropic":
		l.APIKey = anthropicKey
	case "gemini":
		l.APIKey = geminiKey
	case "":
		// Ambiguity resolves toward Anthropic, which is what a deployment that
		// predates the provider switch already expects.
		switch {
		case anthropicKey != "":
			l.Provider, l.APIKey = "anthropic", anthropicKey
		case geminiKey != "":
			l.Provider, l.APIKey = "gemini", geminiKey
		}
	}
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

func setFloatFromEnv(key string, value *float64) {
	if v, ok := os.LookupEnv(key); ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			*value = f
		}
	}
}

// setBoolFromEnv accepts the values strconv.ParseBool does (1/t/true/0/f/false,
// any case). An unparseable value leaves the default in place rather than
// silently reading as false — a typo in QUISAC_AUTO_PR should not quietly
// disable the pipeline's last stage.
func setBoolFromEnv(key string, value *bool) {
	if v, ok := os.LookupEnv(key); ok {
		if b, err := strconv.ParseBool(v); err == nil {
			*value = b
		}
	}
}
