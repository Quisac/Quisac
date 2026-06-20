package github

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
)

// Config holds GitHub App credentials.
type Config struct {
	AppSlug      string
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// ConfigFromEnv loads Config from environment variables.
func ConfigFromEnv() Config {
	return Config{
		AppSlug:      os.Getenv("GITHUB_APP_SLUG"),
		ClientID:     os.Getenv("GITHUB_CLIENT_ID"),
		ClientSecret: os.Getenv("GITHUB_CLIENT_SECRET"),
		RedirectURL:  os.Getenv("GITHUB_REDIRECT_URL"),
	}
}

// Installation is a minimal representation of a GitHub App installation.
type Installation struct {
	ID      int64 `json:"id"`
	Account struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
		Type      string `json:"type"` // "User" or "Organization"
	} `json:"account"`
	RepositorySelection string `json:"repository_selection"`
}

// Handler registers two routes on mux.
func Handler(mux *http.ServeMux, cfg Config) {
	mux.HandleFunc("/github/connect", connectHandler(cfg))
	mux.HandleFunc("/github/callback", callbackHandler(cfg))
}

func connectHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		state, err := randomState()
		if err != nil {
			http.Error(w, "failed to generate state", http.StatusInternalServerError)
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     "quisac_oauth_state",
			Value:    state,
			Path:     "/",
			MaxAge:   300,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})

		installURL := fmt.Sprintf("https://github.com/apps/%s/installations/new?state=%s",
			cfg.AppSlug, url.QueryEscape(state))

		log.Printf("[github] redirecting to: %s", installURL)
		http.Redirect(w, r, installURL, http.StatusFound)
	}
}

func callbackHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[github] callback query: %s", r.URL.RawQuery)

		// 1. Validate state
		cookie, err := r.Cookie("quisac_oauth_state")
		if err != nil || cookie.Value != r.URL.Query().Get("state") {
			log.Printf("[github] state mismatch: cookie err=%v", err)
			http.Error(w, "invalid state", http.StatusBadRequest)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:   "quisac_oauth_state",
			Value:  "",
			Path:   "/",
			MaxAge: -1,
		})

		installationID := r.URL.Query().Get("installation_id")
		if installationID == "" {
			http.Error(w, "missing installation_id", http.StatusBadRequest)
			return
		}
		log.Printf("[github] installation_id: %s", installationID)

		// 2. Exchange OAuth code for user token
		code := r.URL.Query().Get("code")
		if code == "" {
			log.Printf("[github] no code in callback, redirecting without org name")
			http.Redirect(w, r, "/?connected=1", http.StatusFound)
			return
		}

		token, err := exchangeCode(cfg, code)
		if err != nil {
			log.Printf("[github] token exchange failed: %v", err)
			http.Error(w, fmt.Sprintf("token exchange failed: %v", err), http.StatusBadGateway)
			return
		}
		log.Printf("[github] token exchange succeeded")

		// 3. List user installations and find the one matching installation_id.
		//    GET /user/installations/:id does NOT exist — only the list endpoint does.
		installation, err := findInstallation(token, installationID)
		if err != nil {
			log.Printf("[github] findInstallation failed: %v", err)
			// Non-fatal: redirect without org name
			http.Redirect(w, r, "/?connected=1", http.StatusFound)
			return
		}
		log.Printf("[github] installation account: %s (type: %s)", installation.Account.Login, installation.Account.Type)

		// 4. Save session cookie
		if err := saveSession(w, token, installation); err != nil {
			http.Error(w, "session error", http.StatusInternalServerError)
			return
		}

		// 5. Redirect to dashboard with org name
		redirect := "/?connected=1&org=" + url.QueryEscape(installation.Account.Login)
		log.Printf("[github] redirecting to dashboard: %s", redirect)
		http.Redirect(w, r, redirect, http.StatusFound)
	}
}

// findInstallation calls GET /user/installations and returns the entry
// matching installationID. GitHub doesn't expose a single-item endpoint
// for user tokens, so we list and filter.
func findInstallation(token, installationID string) (*Installation, error) {
	req, err := http.NewRequest(http.MethodGet,
		"https://api.github.com/user/installations?per_page=100", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /user/installations returned %d", resp.StatusCode)
	}

	var result struct {
		TotalCount    int            `json:"total_count"`
		Installations []Installation `json:"installations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	log.Printf("[github] user has %d installation(s)", result.TotalCount)

	for i := range result.Installations {
		inst := &result.Installations[i]
		if fmt.Sprintf("%d", inst.ID) == installationID {
			return inst, nil
		}
	}

	return nil, fmt.Errorf("installation %s not found among %d user installations",
		installationID, result.TotalCount)
}

func exchangeCode(cfg Config, code string) (string, error) {
	params := url.Values{
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
		"code":          {code},
		"redirect_uri":  {cfg.RedirectURL},
	}

	req, err := http.NewRequest(http.MethodPost,
		"https://github.com/login/oauth/access_token", nil)
	if err != nil {
		return "", err
	}
	req.URL.RawQuery = params.Encode()
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("%s: %s", result.Error, result.ErrorDesc)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("empty access token in response")
	}
	return result.AccessToken, nil
}

type session struct {
	Token        string        `json:"token"`
	Installation *Installation `json:"installation"`
}

func saveSession(w http.ResponseWriter, token string, inst *Installation) error {
	s := session{Token: token, Installation: inst}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "quisac_gh",
		Value:    hex.EncodeToString(b),
		Path:     "/",
		MaxAge:   60 * 60 * 24 * 7,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
