package github

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v75/github"
)

type Repo struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	HTMLURL       string `json:"html_url"`
	DefaultBranch string `json:"default_branch"`
}

func (a *App) installClient(installationID string) (*github.Client, error) {
	id, err := strconv.ParseInt(installationID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("github: invalid installation id %q: %w", installationID, err)
	}
	itr := ghinstallation.NewFromAppsTransport(a.appsTransport, id)
	return github.NewClient(&http.Client{Transport: itr}), nil
}

func (a *App) Repositories(ctx context.Context, installationID string) ([]Repo, error) {
	client, err := a.installClient(installationID)
	if err != nil {
		return nil, err
	}

	opt := &github.ListOptions{PerPage: 100}
	var repos []Repo
	for {
		result, resp, err := client.Apps.ListRepos(ctx, opt)
		if err != nil {
			return nil, fmt.Errorf("github: list installation repos: %w", err)
		}
		for _, r := range result.Repositories {
			repos = append(repos, Repo{
				ID:            r.GetID(),
				FullName:      r.GetFullName(),
				Private:       r.GetPrivate(),
				HTMLURL:       r.GetHTMLURL(),
				DefaultBranch: r.GetDefaultBranch(),
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return repos, nil
}

func (a *App) DownloadRepo(
	ctx context.Context,
	installationID,
	fullName string,
) (dir string, cleanup func(), err error) {
	owner, repo, ok := splitFullName(fullName)
	if !ok {
		return "", nil, fmt.Errorf("DownloadRepo: fullName must be owner/repo, got %q", fullName)
	}

	client, err := a.installClient(installationID)
	if err != nil {
		return "", nil, fmt.Errorf("DownloadRepo: %w", err)
	}

	archiveURL, _, err := client.Repositories.GetArchiveLink(
		ctx, owner, repo, github.Zipball, nil, 5,
	)
	if err != nil {
		return "", nil, fmt.Errorf("DownloadRepo: archive link: %w", err)
	}

		return "", nil, err
	}

	httpClient := &http.Client{Timeout: 10 * time.Minute}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("DownloadRepo: fetch zip: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", nil, fmt.Errorf(
			"DownloadRepo: GitHub returned %s: %s",
			resp.Status,
			string(body),
		)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf(
			"DownloadRepo: read body: %w",
			err,
		)
	}

	zr, err := zip.NewReader(
		bytes.NewReader(data),
		int64(len(data)),
	)
	if err != nil {
		return "", nil, fmt.Errorf(
			"DownloadRepo: open zip: %w",
			err,
		)
	}

	tmpDir, err := os.MkdirTemp("", "quisac-repo-*")
	if err != nil {
		return "", nil, fmt.Errorf(
			"DownloadRepo: mktemp: %w",
			err,
		)
	}

	cleanup = func() {
		_ = os.RemoveAll(tmpDir)
	}

	for _, f := range zr.File {
		clean := filepath.Clean(f.Name)

		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			continue
		}

		dest := filepath.Join(tmpDir, clean)

		if !strings.HasPrefix(dest, filepath.Clean(tmpDir)+string(os.PathSeparator)) {
			continue
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				cleanup()
				return "", nil, err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			cleanup()
			return "", nil, err
		}

		rc, err := f.Open()
		if err != nil {
			cleanup()
			return "", nil, err
		}

		out, err := os.OpenFile(
			dest,
			os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
			f.Mode(),
		)
		if err != nil {
			rc.Close()
			cleanup()
			return "", nil, err
		}

		_, err = io.Copy(out, rc)

		closeErr1 := rc.Close()
		closeErr2 := out.Close()

		if err != nil {
			cleanup()
			return "", nil, err
		}

		if closeErr1 != nil {
			cleanup()
			return "", nil, closeErr1
		}

		if closeErr2 != nil {
			cleanup()
			return "", nil, closeErr2
		}
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return tmpDir, cleanup, nil
	}

	if len(entries) == 1 && entries[0].IsDir() {
		return filepath.Join(tmpDir, entries[0].Name()), cleanup, nil
	}

	return tmpDir, cleanup, nil
}

func splitFullName(fullName string) (owner, repo string, ok bool) {
	owner, repo, ok = strings.Cut(fullName, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", false
	}
	return owner, repo, true
}
