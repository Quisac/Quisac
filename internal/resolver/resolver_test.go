package resolver_test

import (
	"testing"

	"quisac/internal/resolver"
)

func TestResolveImport(t *testing.T) {
	files := map[string]bool{
		"main.py": true, "utils.py": true,
		"src/a.py": true, "src/b.py": true,
		"web/app.js": true, "web/util.js": true,
	}

	cases := []struct {
		name, fromFile, rawImport, ext, want string
	}{
		{"py to repo root", "main.py", "from utils import helper", ".py", "utils.py"},
		{"py dotted path", "src/a.py", "import src.b", ".py", "src/b.py"},
		{"py external", "main.py", "import os", ".py", ""},
		{"js relative ext probe", "web/app.js", `import x from "./util"`, ".js", "web/util.js"},
		{"js external package", "web/app.js", `import React from "react"`, ".js", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolver.ResolveImport(files, tc.fromFile, tc.rawImport, tc.ext); got != tc.want {
				t.Errorf("ResolveImport(%q, %q) = %q, want %q", tc.fromFile, tc.rawImport, got, tc.want)
			}
		})
	}
}
