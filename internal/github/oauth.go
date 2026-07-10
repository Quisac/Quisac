package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/go-github/v75/github"
	"golang.org/x/oauth2"
)

func (a *App) AuthorizeURL(state string) string {
	return a.oauth.AuthCodeURL(state)
}

func (a *App) ExchangeCode(ctx context.Context, code string) (string, error) {
	tok, err := a.oauth.Exchange(ctx, code)
	if err != nil {
		return "", fmt.Errorf("github: token exchange: %w", err)
	}
	return tok.AccessToken, nil
}

func (a *App) userClient(ctx context.Context, userToken string) *github.Client {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: userToken})
	return github.NewClient(oauth2.NewClient(ctx, ts))
}

// User returns the authenticated user's stable numeric id and login.
//
// A just-minted GitHub App user-to-server token can briefly 401 on the very
// first authorization, before the new grant has propagated. We retry a 401 a
// few times with backoff; any other error fails fast.
func (a *App) User(ctx context.Context, userToken string) (int64, string, error) {
	client := a.userClient(ctx, userToken)

	backoff := []time.Duration{0, 1 * time.Second, 2 * time.Second, 3 * time.Second}
	var lastErr error
	for attempt, wait := range backoff {
		if wait > 0 {
			select {
			case <-ctx.Done():
				return 0, "", ctx.Err()
			case <-time.After(wait):
			}
		}

		u, _, err := client.Users.Get(ctx, "")
		if err == nil {
			return u.GetID(), u.GetLogin(), nil
		}
		lastErr = err

		if !isUnauthorized(err) {
			break // not a propagation delay — real failure, stop retrying
		}
		fmt.Printf("get user: 401 on attempt %d/%d, retrying\n", attempt+1, len(backoff))
	}
	return 0, "", fmt.Errorf("github: get user: %w", lastErr)
}

// isUnauthorized reports whether err is a GitHub 401 response.
func isUnauthorized(err error) bool {
	var ghErr *github.ErrorResponse
	if errors.As(err, &ghErr) && ghErr.Response != nil {
		return ghErr.Response.StatusCode == http.StatusUnauthorized
	}
	return false
}

type Installation struct {
	ID      int64
	Account struct {
		Login string
	}
}

func (a *App) UserInstallations(ctx context.Context, userToken string) ([]Installation, error) {
	client := a.userClient(ctx, userToken)

	opt := &github.ListOptions{PerPage: 100}
	var out []Installation
	for {
		insts, resp, err := client.Apps.ListUserInstallations(ctx, opt)
		if err != nil {
			return nil, fmt.Errorf("github: list user installations: %w", err)
		}
		for _, in := range insts {
			var i Installation
			i.ID = in.GetID()
			i.Account.Login = in.GetAccount().GetLogin()
			out = append(out, i)
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return out, nil
}
