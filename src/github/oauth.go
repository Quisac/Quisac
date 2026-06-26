package github

import (
	"context"
	"fmt"

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

func (a *App) User(ctx context.Context, userToken string) (string, error) {
	u, _, err := a.userClient(ctx, userToken).Users.Get(ctx, "")
	if err != nil {
		return "", fmt.Errorf("github: get user: %w", err)
	}
	return u.GetLogin(), nil
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
