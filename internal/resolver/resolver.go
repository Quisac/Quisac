package resolver

import (
	"path/filepath"
	"strings"
)

// ResolveImport resolves a raw import string (as extracted by the AST parsers)
// to a repo-relative file path. Returns "" for external dependencies (npm
// packages, stdlib, PyPI) that don't exist in the repo.
//
// files     — the set of repo-relative paths of every source file in the repo
//
//	(the "manifest"). Membership in this set is how we decide whether
//	an import points at an internal file. This replaces hitting the
//	filesystem, so resolution works identically for a freshly-walked
//	tarball and for an incremental update where only the manifest
//	(not the working tree) is available.
//
// fromFile  — repo-relative path of the file that contains the import
// rawImport — the raw import text as captured by the AST parser
// ext       — file extension of the importing file (.py, .js, .ts, etc.)
func ResolveImport(files map[string]bool, fromFile, rawImport, ext string) string {
	switch ext {
	case ".py":
		mod := parsePythonModule(rawImport)
		if mod == "" {
			return ""
		}
		return resolvePython(files, fromFile, mod)

	case ".js", ".jsx", ".ts", ".tsx":
		mod := parseJSModule(rawImport)
		if mod == "" {
			return ""
		}
		return resolveJS(files, fromFile, mod)
	}
	return ""
}

func parsePythonModule(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "from ") {
		parts := strings.Fields(raw)
		if len(parts) >= 2 {
			return parts[1]
		}
	}
	if strings.HasPrefix(raw, "import ") {
		parts := strings.Fields(raw)
		if len(parts) >= 2 {
			return strings.Split(parts[1], ",")[0]
		}
	}
	return ""
}

func resolvePython(files map[string]bool, fromFile, module string) string {
	fromDir := filepath.Dir(fromFile)

	if strings.HasPrefix(module, ".") {
		dots := 0
		for _, c := range module {
			if c == '.' {
				dots++
			} else {
				break
			}
		}
		rest := module[dots:]
		base := fromDir
		for i := 1; i < dots; i++ {
			base = filepath.Dir(base)
		}
		relPath := strings.ReplaceAll(rest, ".", "/")
		return tryPythonPaths(files, base, relPath)
	}

	// Absolute import — convert dot notation to path and try candidate locations.
	modPath := strings.ReplaceAll(module, ".", "/")

	// 1. Directly from repo root (most common in monorepos)
	if p := tryPythonPaths(files, "", modPath); p != "" {
		return p
	}
	// 2. From common source roots used in Python monorepos
	for _, srcDir := range []string{"src", "app", "lib", "backend"} {
		if p := tryPythonPaths(files, srcDir, modPath); p != "" {
			return p
		}
	}
	// 3. Sibling directory of the importing file (for non-root-relative imports)
	if p := tryPythonPaths(files, fromDir, modPath); p != "" {
		return p
	}

	return "" // external PyPI package
}

// tryPythonPaths checks whether the module exists as a .py file or a package
// (__init__.py) rooted at base/rel, against the repo manifest.
func tryPythonPaths(files map[string]bool, base, rel string) string {
	candidate := base
	if rel != "" {
		candidate = filepath.Join(base, rel)
	}
	// Direct module file: services/user.py
	if p := norm(candidate + ".py"); files[p] {
		return p
	}
	// Package directory: services/user/__init__.py
	if p := norm(filepath.Join(candidate, "__init__.py")); files[p] {
		return p
	}
	return ""
}

// ── JavaScript / TypeScript ───────────────────────────────────────────────────

// parseJSModule extracts the module path from a raw JS/TS import string.
//
//	`import { X } from '../services/user'` → "../services/user"
//	`import X from "./utils"`              → "./utils"
//	`const x = require('./helpers')`       → "./helpers"
//
// The module path is always the last string literal in an import statement.
func parseJSModule(raw string) string {
	for i := len(raw) - 1; i >= 0; i-- {
		q := raw[i]
		if q == '\'' || q == '"' || q == '`' {
			for j := i - 1; j >= 0; j-- {
				if raw[j] == q {
					mod := raw[j+1 : i]
					// Sanity check: must look like a path or package name
					if mod != "" && !strings.ContainsAny(mod, "\n\r") {
						return mod
					}
				}
			}
		}
	}
	return ""
}

// jsExts is the precedence order when probing for a file without an extension.
var jsExts = []string{".ts", ".tsx", ".js", ".jsx"}

func resolveJS(files map[string]bool, fromFile, module string) string {
	fromDir := filepath.Dir(fromFile)

	var base string
	if strings.HasPrefix(module, ".") || strings.HasPrefix(module, "/") {
		// Relative or absolute-path import
		base = filepath.Join(fromDir, module)
	} else {
		// Bare specifier — could be:
		//   a) A monorepo workspace package  e.g. "@acme/shared" or "shared"
		//   b) An external npm package (most common)
		//
		// We probe the repo root. If nothing is found we treat it as external.
		tryPath := module
		if strings.HasPrefix(module, "@") {
			// @scope/pkg → strip leading @ so we can try repoRoot/@scope/pkg
			tryPath = strings.TrimPrefix(module, "@")
		}
		base = tryPath
	}

	return tryJSPaths(files, base)
}

// tryJSPaths probes for a JS/TS file at the given base path against the repo
// manifest, trying common extension and index-file conventions.
func tryJSPaths(files map[string]bool, base string) string {
	// Already a resolvable path with extension
	if p := norm(base); files[p] {
		return p
	}
	// Try appending extensions: utils → utils.ts, utils.js, …
	for _, ext := range jsExts {
		if p := norm(base + ext); files[p] {
			return p
		}
	}
	// Try as a directory's index file: utils/ → utils/index.ts, …
	for _, ext := range jsExts {
		if p := norm(filepath.Join(base, "index"+ext)); files[p] {
			return p
		}
	}
	return ""
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// norm canonicalizes a path to the forward-slash, cleaned form used as manifest
// keys, so resolution matches regardless of the host separator.
func norm(p string) string {
	return filepath.ToSlash(filepath.Clean(p))
}
