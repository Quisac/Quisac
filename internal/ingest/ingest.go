// Package ingest orchestrates building and updating a repository's code graph.
// It is transport-agnostic: the HTTP server calls into it, and it depends only
// on the narrow RepoSource and GraphStore interfaces it declares here.
package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"quisac/internal/core"
	"quisac/internal/filewalk"
	"quisac/internal/resolver"
)

// RepoSource fetches repository contents. Satisfied by *github.App.
type RepoSource interface {
	DownloadRepo(ctx context.Context, installationID, repo string) (dir string, cleanup func(), err error)
	GetFileContent(ctx context.Context, installationID, repo, path, ref string) (content []byte, found bool, err error)
	HeadSHA(ctx context.Context, installationID, repo string) (string, error)
}

// GraphStore persists code graphs. Satisfied by *graphstore.Store.
type GraphStore interface {
	SaveGraph(ctx context.Context, repo, headSHA string, g *core.Graph) error
	ReplaceFile(ctx context.Context, repo, path string, g *core.Graph) error
	DeleteFile(ctx context.Context, repo, path string) error
	SetImports(ctx context.Context, repo, fromPath string, targets []string) error
	RebuildImports(ctx context.Context, repo string, edges [][2]string) error
	FileManifest(ctx context.Context, repo string) (map[string]bool, error)
	RawImports(ctx context.Context, repo string) (map[string][]string, error)
	GetHead(ctx context.Context, repo string) (string, error)
	SetHead(ctx context.Context, repo, headSHA string) error
}

// Indexer builds full indexes and applies incremental updates. Per-repo
// serialization is enforced upstream by the durable job queue (at most one
// running job per repo), so ProcessPush can assume it has no concurrent writer.
type Indexer struct {
	src   RepoSource
	store GraphStore
	log   *slog.Logger
}

func New(src RepoSource, store GraphStore, log *slog.Logger) *Indexer {
	if log == nil {
		log = slog.Default()
	}
	return &Indexer{src: src, store: store, log: log}
}

// FullIndex downloads repo, builds its graph, and persists it under headSHA
// (looked up from GitHub when empty). It returns the built graph.
func (ix *Indexer) FullIndex(ctx context.Context, installationID, repo, headSHA string) (*core.Graph, error) {
	dir, cleanup, err := ix.src.DownloadRepo(ctx, installationID, repo)
	if err != nil {
		return nil, fmt.Errorf("ingest: download %s: %w", repo, err)
	}
	defer cleanup()

	g := filewalk.ReadRepo(dir)

	if headSHA == "" {
		if sha, err := ix.src.HeadSHA(ctx, installationID, repo); err != nil {
			ix.log.Warn("head sha lookup failed", "repo", repo, "err", err)
		} else {
			headSHA = sha
		}
	}

	if err := ix.store.SaveGraph(ctx, repo, headSHA, g); err != nil {
		return nil, err
	}
	ix.log.Info("full index", "repo", repo, "head", shortSHA(headSHA),
		"nodes", len(g.Nodes), "edges", len(g.Edges))
	return g, nil
}

// ProcessPush brings repo's graph up to date for a push. When the stored head
// matches before and the change set is complete, it applies a surgical
// incremental update touching only changed files; otherwise it re-indexes fully.
//
// changed is the raw union of paths a push touched; complete is false when the
// webhook payload may have been truncated (too many commits).
func (ix *Indexer) ProcessPush(ctx context.Context, installationID, repo, before, after string, changed []string, complete bool) error {
	head, err := ix.store.GetHead(ctx, repo)
	if err != nil {
		return err
	}

	if head == "" || head != before || !complete {
		ix.log.Info("push → full reindex", "repo", repo, "after", shortSHA(after),
			"head", shortSHA(head), "before", shortSHA(before), "complete", complete)
		_, err := ix.FullIndex(ctx, installationID, repo, after)
		return err
	}

	paths := indexablePaths(changed)
	if len(paths) == 0 {
		return ix.store.SetHead(ctx, repo, after)
	}

	manifest, err := ix.store.FileManifest(ctx, repo)
	if err != nil {
		return err
	}

	structural := false // a file was added or removed → manifest changed
	changedImports := map[string][]string{}
	for _, path := range paths {
		content, found, err := ix.src.GetFileContent(ctx, installationID, repo, path, after)
		if err != nil {
			ix.log.Warn("fetch file failed", "repo", repo, "path", path, "err", err)
			continue // transient — leave this file as-is
		}
		if !found || len(content) > filewalk.MaxFileBytes {
			if manifest[path] {
				structural = true
				delete(manifest, path)
			}
			if err := ix.store.DeleteFile(ctx, repo, path); err != nil {
				return err
			}
			continue
		}

		sub, err := filewalk.ParseFile(path, content)
		if err != nil {
			ix.log.Warn("parse file failed", "repo", repo, "path", path, "err", err)
			if manifest[path] {
				structural = true
				delete(manifest, path)
			}
			if err := ix.store.DeleteFile(ctx, repo, path); err != nil {
				return err
			}
			continue
		}
		if err := ix.store.ReplaceFile(ctx, repo, path, sub); err != nil {
			return err
		}
		if !manifest[path] {
			structural = true
			manifest[path] = true
		}
		changedImports[path] = sub.RawImports[path]
	}

	if err := ix.recomputeImports(ctx, repo, manifest, changedImports, structural); err != nil {
		return err
	}

	if err := ix.store.SetHead(ctx, repo, after); err != nil {
		return err
	}
	ix.log.Info("push → incremental", "repo", repo, "before", shortSHA(before),
		"after", shortSHA(after), "changed", len(paths), "structural", structural)
	return nil
}

// recomputeImports refreshes file→file import edges. A modify-only push only
// re-resolves the changed files' outbound imports; a structural push (files
// added/removed) rebuilds the whole import layer since inbound edges can shift.
func (ix *Indexer) recomputeImports(ctx context.Context, repo string, manifest map[string]bool, changedImports map[string][]string, structural bool) error {
	if !structural {
		for path, imports := range changedImports {
			if err := ix.store.SetImports(ctx, repo, path, resolveTargets(path, imports, manifest)); err != nil {
				return err
			}
		}
		return nil
	}

	raw, err := ix.store.RawImports(ctx, repo)
	if err != nil {
		return err
	}
	var edges [][2]string
	for path, imports := range raw {
		for _, target := range resolveTargets(path, imports, manifest) {
			edges = append(edges, [2]string{path, target})
		}
	}
	return ix.store.RebuildImports(ctx, repo, edges)
}

// resolveTargets resolves a file's raw imports to the de-duplicated set of
// repo-relative paths that exist in the manifest.
func resolveTargets(path string, imports []string, manifest map[string]bool) []string {
	ext := filepath.Ext(path)
	seen := map[string]bool{}
	var out []string
	for _, raw := range imports {
		if t := resolver.ResolveImport(manifest, path, raw, ext); t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// indexablePaths returns the de-duplicated, indexable subset of changed paths.
func indexablePaths(changed []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range changed {
		if seen[p] || !filewalk.IsIndexable(p) {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
