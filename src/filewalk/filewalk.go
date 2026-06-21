package filewalk

import (
	"fmt"
	"os"
	"path/filepath"
	"quisac/src/astparsers"
	"quisac/src/core"
	"quisac/src/resolver"
	"quisac/src/treesitter"
)

var supported = map[string]bool{
	".py": true, ".go": true,
	".js": true, ".jsx": true,
	".ts": true, ".tsx": true,
}

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

	// ── Pass 1: AST extraction ────────────────────────────────────────────────
	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "__pycache__", "vendor", "dist", "build":
				return filepath.SkipDir
			}
			return nil
		}

		ext := filepath.Ext(path)
		if !supported[ext] {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			fmt.Printf("error reading %s: %v\n", path, err)
			return nil
		}

		extracted, err := treesitter.ParseCode(content, ext, path)
		if err != nil {
			fmt.Printf("error parsing %s: %v\n", path, err)
			return nil
		}

		graph.AddFile(extracted)
		allExtracted = append(allExtracted, extracted)
		return nil
	})

	if err != nil {
		fmt.Printf("error walking %s: %v\n", repoRoot, err)
	}

	// ── Pass 2: Import resolution ─────────────────────────────────────────────
	// Now that every file node exists in the graph, resolve raw import strings
	// to concrete file paths and add direct file→file edges.
	resolved, unresolved := 0, 0
	for _, ex := range allExtracted {
		ext := filepath.Ext(ex.FilePath)
		for _, rawImport := range ex.Imports {
			target := resolver.ResolveImport(repoRoot, ex.FilePath, rawImport, ext)
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
