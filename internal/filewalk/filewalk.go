package filewalk

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"quisac/internal/astparsers"
	"quisac/internal/core"
	"quisac/internal/resolver"
	"quisac/internal/treesitter"
)

var supported = map[string]bool{
	".py": true, ".go": true,
	".js": true, ".jsx": false,
	".ts": true, ".tsx": false,
}

// skipDirs hold dependencies or build output, never first-party source. Parsing
// them produces enormous, meaningless symbols.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "__pycache__": true,
	"vendor": true, "dist": true, "build": true,
	".next": true, ".nuxt": true, ".svelte-kit": true,
	"out": true, "coverage": true, ".cache": true, ".turbo": true,
}

// MaxFileBytes skips files too large to be hand-written source.
const MaxFileBytes = 1 << 20 // 1 MiB

// ReadRepo builds a dependency graph from every supported source file under dir,
// in three passes:
//
//  1. Scan — collect the paths worth parsing, reading none of them.
//  2. AST — parse those files across a bounded worker pool, then assemble the
//     graph on one goroutine in scan order, so node insertion is deterministic.
//  3. Resolution — resolve each file's raw imports against the files actually
//     parsed. A match becomes a file→file Imports edge, which is what makes the
//     graph traversable across module boundaries.
func ReadRepo(repoRoot string) *core.Graph {
	paths := scanRepo(repoRoot)
	extracted := parseAll(repoRoot, paths)

	graph := core.NewGraph()
	// The manifest. Resolving against it rather than the filesystem keeps node
	// IDs stable across re-downloads, and serves incremental updates unchanged.
	files := make(map[string]bool, len(extracted))
	for _, ex := range extracted {
		if ex == nil {
			continue
		}
		graph.AddFile(ex)
		files[ex.FilePath] = true
	}

	// Pass 3: import resolution.
	resolved, unresolved := 0, 0
	for _, ex := range extracted {
		if ex == nil {
			continue
		}
		ext := filepath.Ext(ex.FilePath)
		for _, rawImport := range ex.Imports {
			target := resolver.ResolveImport(files, ex.FilePath, rawImport, ext)
			if target != "" {
				graph.AddImportEdge(ex.FilePath, target)
				resolved++
			} else {
				unresolved++
			}
		}
	}

	slog.Info("repo indexed", "files", len(files), "nodes", len(graph.Nodes),
		"edges", len(graph.Edges), "imports_resolved", resolved, "imports_external", unresolved)
	return graph
}

// scanRepo returns the absolute paths of every file ReadRepo should parse.
func scanRepo(repoRoot string) []string {
	var paths []string
	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !supported[filepath.Ext(path)] {
			return nil
		}
		// Both are bundle output, not source.
		if isMinified(info.Name()) || info.Size() > MaxFileBytes {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		slog.Warn("repo walk failed", "root", repoRoot, "err", err)
	}
	return paths
}

// parseAll parses paths concurrently, returning one result per input in the same
// order, nil where a file could not be read or parsed.
//
// Each worker owns a tree-sitter session: the parsers are stateful and cannot be
// shared, and building one per worker keeps setup out of the hot loop.
func parseAll(repoRoot string, paths []string) []*astparsers.Extracted {
	out := make([]*astparsers.Extracted, len(paths))
	if len(paths) == 0 {
		return out
	}

	workers := min(runtime.NumCPU(), len(paths))
	idx := make(chan int)
	var wg sync.WaitGroup

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session := treesitter.NewSession()
			defer session.Close()

			for i := range idx {
				path := paths[i]
				content, err := os.ReadFile(path)
				if err != nil {
					slog.Warn("read file failed", "path", path, "err", err)
					continue
				}
				rel := relPath(repoRoot, path)
				ex, err := session.Parse(content, filepath.Ext(path), rel)
				if err != nil {
					slog.Warn("parse file failed", "path", rel, "err", err)
					continue
				}
				out[i] = ex
			}
		}()
	}

	for i := range paths {
		idx <- i
	}
	close(idx)
	wg.Wait()
	return out
}

// isMinified reports whether name looks like a minified/generated bundle, e.g.
// "vendor.min.js" or "chunk.min.css".
func isMinified(name string) bool {
	return strings.Contains(name, ".min.")
}

// inSkipDir reports whether a repo-relative path lives under a dependency or
// build directory that ReadRepo would skip.
func inSkipDir(path string) bool {
	for seg := range strings.SplitSeq(path, "/") {
		if skipDirs[seg] {
			return true
		}
	}
	return false
}

// IsIndexable reports whether a path is first-party source: a supported
// extension, not minified, not under a skipped directory. Shared by the full
// walk and incremental updates.
func IsIndexable(path string) bool {
	path = filepath.ToSlash(path)
	return supported[filepath.Ext(path)] &&
		!isMinified(filepath.Base(path)) &&
		!inSkipDir(path)
}

// ParseFile builds a single-file subgraph: nodes and intra-file edges only.
// Cross-file imports are resolved by the caller against the repo manifest.
func ParseFile(path string, content []byte) (*core.Graph, error) {
	path = filepath.ToSlash(path)
	extracted, err := treesitter.ParseCode(content, filepath.Ext(path), path)
	if err != nil {
		return nil, err
	}
	g := core.NewGraph()
	g.AddFile(extracted)
	return g, nil
}

// relPath returns path relative to repoRoot in forward-slash form, falling back
// to the cleaned original.
func relPath(repoRoot, path string) string {
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil {
		return filepath.ToSlash(filepath.Clean(path))
	}
	return filepath.ToSlash(rel)
}
