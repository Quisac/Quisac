// normalizer.go
// Author: Abhi
//
// This file is responsible for cleaning up the raw dependency graph
// produced by the graph builder. It does two things:
//
// 1. Normalizes file paths into dot-notation module names
//    e.g. src/services/payment.go → src.services.payment
//    so that node names in the graph match how modules reference each other.
//
// 2. Filters import edges to internal-only dependencies.
//    When a file imports os, fmt, or a third-party package like fastapi,
//    those get dropped. Only imports that map to a file inside the repo
//    are kept as edges in the graph.
//
// Output of this file feeds directly into the graph as clean nodes and edges.

package normalizer

import (
	"path/filepath"
	"strings"
)

// PathToModule converts a file path to a dot-notation module name.
// e.g. src/services/payment.go → src.services.payment
func PathToModule(filePath, repoRoot string) string {
	rel, err := filepath.Rel(repoRoot, filePath)
	if err != nil {
		return ""
	}

	// strip the file extension
	ext := filepath.Ext(rel)
	rel = strings.TrimSuffix(rel, ext)

	// convert path separators to dots
	return strings.ReplaceAll(rel, string(filepath.Separator), ".")
}

// FilterInternal takes a list of imported module names and returns
// only the ones that exist as nodes in the graph (internal deps).
// Anything not in the graph — stdlib, third-party — gets dropped.
func FilterInternal(imports []string, graphNodes map[string]bool) []string {
	internal := []string{}
	for _, imp := range imports {
		if graphNodes[imp] {
			internal = append(internal, imp)
		}
	}
	return internal
}

// BuildNodeSet builds a set of all module names from a list of file paths.
// Used to check if an import is internal.
func BuildNodeSet(filePaths []string, repoRoot string) map[string]bool {
	nodes := map[string]bool{}
	for _, fp := range filePaths {
		module := PathToModule(fp, repoRoot)
		if module != "" {
			nodes[module] = true
		}
	}
	return nodes
}
