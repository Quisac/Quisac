package github

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

var GitHubAPIURL = "https://api.github.com"

type Service struct {
	cfg    Config
	client *http.Client
}

func NewService(cfg Config) *Service {
	return &Service{
		cfg:    cfg,
		client: http.DefaultClient,
	}
}

func (s *Service) InstallURL(state string) string {
	return fmt.Sprintf(
		"https://github.com/apps/%s/installations/new?state=%s",
		s.cfg.AppSlug,
		url.QueryEscape(state),
	)
}

func (s *Service) ExchangeCode(code string) (string, error) {
	params := url.Values{
		"client_id":     {s.cfg.ClientID},
		"client_secret": {s.cfg.ClientSecret},
		"code":          {code},
		"redirect_uri":  {s.cfg.RedirectURL},
	}

	req, err := http.NewRequest(
		http.MethodPost,
		"https://github.com/login/oauth/access_token",
		nil,
	)
	if err != nil {
		return "", err
	}

	req.URL.RawQuery = params.Encode()
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
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
		return "", err
	}

	if result.Error != "" {
		return "", fmt.Errorf("%s: %s", result.Error, result.ErrorDesc)
	}

	if result.AccessToken == "" {
		return "", fmt.Errorf("missing access token")
	}

	return result.AccessToken, nil
}

func (s *Service) FindInstallation(
	token string,
	installationID string,
) (*Installation, error) {
	req, err := http.NewRequest(
		http.MethodGet,
		GitHubAPIURL+"/user/installations",
		nil,
	)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Installations []Installation `json:"installations"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	for _, inst := range result.Installations {
		if fmt.Sprintf("%d", inst.ID) == installationID {
			return &inst, nil
		}
	}

	return nil, fmt.Errorf("installation not found")
}

func RandomState() (string, error) {
	bytes := make([]byte, 16)

	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}

	return hex.EncodeToString(bytes), nil
}
