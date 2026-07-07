package filewalk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"quisac/internal/astparsers"
	"quisac/internal/core"
	"quisac/internal/resolver"
	"quisac/internal/treesitter"
)

var supported = map[string]bool{
	".py": true, ".go": true,
	".js": true, ".jsx": true,
	".ts": true, ".tsx": true,
}

// skipDirs are directories that hold dependencies or generated/build output —
// never first-party source. Parsing them (especially minified bundles under
// .next/dist) produces enormous, meaningless symbols.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "__pycache__": true,
	"vendor": true, "dist": true, "build": true,
	".next": true, ".nuxt": true, ".svelte-kit": true,
	"out": true, "coverage": true, ".cache": true, ".turbo": true,
}

// MaxFileBytes skips files too large to be hand-written source. Minified or
// generated blobs blow past this; real source files effectively never do.
const MaxFileBytes = 1 << 20 // 1 MiB

// ReadRepo builds a dependency graph from all supported source files under dir.
//
// It runs in two passes:
//
//  1. AST pass — every file is parsed and its symbols (functions, classes,
//     imports, calls) are added to the graph as nodes and raw edges.
//
//  2. Resolution pass — each file's raw import strings are resolved against
//     the actual filesystem. When a match is found inside the repo, a direct
//     file→file "Imports" edge is added, making the graph traversable across
//     module boundaries.
func ReadRepo(repoRoot string) *core.Graph {
	graph := core.NewGraph()

	var allExtracted []*astparsers.Extracted
	// files is the repo manifest: the set of every parsed file's repo-relative
	// path. Import resolution (Pass 2) is done against this set rather than the
	// filesystem, so node IDs are stable across re-downloads and the same logic
	// serves incremental updates that only have the manifest.
	files := map[string]bool{}

	// ── Pass 1: AST extraction ────────────────────────────────────────────────
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

		ext := filepath.Ext(path)
		if !supported[ext] {
			return nil
		}

		// Skip minified/generated files (e.g. app.min.js) and anything too large
		// to be hand-written — both are bundle output, not source.
		if isMinified(info.Name()) || info.Size() > MaxFileBytes {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			fmt.Printf("error reading %s: %v\n", path, err)
			return nil
		}

		rel := relPath(repoRoot, path)

		extracted, err := treesitter.ParseCode(content, ext, rel)
		if err != nil {
			fmt.Printf("error parsing %s: %v\n", path, err)
			return nil
		}

		graph.AddFile(extracted)
		allExtracted = append(allExtracted, extracted)
		files[rel] = true
		return nil
	})

	if err != nil {
		fmt.Printf("error walking %s: %v\n", repoRoot, err)
	}

	// ── Pass 2: Import resolution ─────────────────────────────────────────────
	// Now that the manifest is complete, resolve raw import strings to concrete
	// repo-relative paths and add direct file→file edges.
	resolved, unresolved := 0, 0
	for _, ex := range allExtracted {
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

	fmt.Printf("import resolution: %d resolved, %d external/unresolved\n", resolved, unresolved)
	return graph
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

// IsIndexable reports whether a repo-relative path is a first-party source file
// we index: a supported extension, not minified, and not under a skipped
// directory. It's the single predicate shared by the full walk and incremental
// updates.
func IsIndexable(path string) bool {
	path = filepath.ToSlash(path)
	return supported[filepath.Ext(path)] &&
		!isMinified(filepath.Base(path)) &&
		!inSkipDir(path)
}

// ParseFile builds a single-file subgraph — the file's nodes and intra-file
// edges — from its content. It carries no cross-file import edges; those are
// resolved by the caller against the repo manifest.
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

// relPath returns path relative to repoRoot in forward-slash form. It falls back
// to the cleaned original path if a relative path can't be computed.
func relPath(repoRoot, path string) string {
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil {
		return filepath.ToSlash(filepath.Clean(path))
	}
	return filepath.ToSlash(rel)
}
