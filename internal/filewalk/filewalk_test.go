package filewalk_test

import (
	"os"
	"path/filepath"
	"quisac/internal/filewalk"
	"testing"
)

func TestReadRepo(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Create a python file with an import
	pyFile := filepath.Join(tempDir, "main.py")
	pyContent := `
import os
from utils import helper

class MainApp:
    def run(self):
        helper()
`
	if err := os.WriteFile(pyFile, []byte(pyContent), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. Create the target of the import
	utilsFile := filepath.Join(tempDir, "utils.py")
	utilsContent := `
def helper():
    print("helper")
`
	if err := os.WriteFile(utilsFile, []byte(utilsContent), 0644); err != nil {
		t.Fatal(err)
	}

	// 3. Walk the directory and generate the graph
	g := filewalk.ReadRepo(tempDir)
	if g == nil {
		t.Fatal("Expected graph, got nil")
	}

	if len(g.Nodes) == 0 {
		t.Errorf("Graph has no nodes")
	}

	// 4. Verify that the import resolution worked and created an edge.
	// Node IDs use repo-relative paths, so main.py imports utils.py.
	hasImportEdge := false
	for _, edge := range g.Edges {
		if edge.Type == "Imports" && edge.From == "file:main.py" && edge.To == "file:utils.py" {
			hasImportEdge = true
			break
		}
	}

	if !hasImportEdge {
		t.Errorf("Expected an Imports edge from main.py to utils.py, but it was not found")
	}
}

// ReadRepo parses across a worker pool, so the same tree must still produce the
// same graph — same nodes, same edges, in the same order.
func TestReadRepoIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []struct{ name, body string }{
		{"a.py", "from b import helper\n\ndef run():\n    helper()\n"},
		{"b.py", "import os\n\ndef helper():\n    return os.getenv('X')\n"},
		{"c.py", "from a import run\n\nclass Job:\n    def go(self):\n        run()\n"},
		{"d.py", "def unused():\n    pass\n"},
	} {
		if err := os.WriteFile(filepath.Join(dir, f.name), []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	first := filewalk.ReadRepo(dir)
	for i := range 5 {
		g := filewalk.ReadRepo(dir)
		if len(g.Nodes) != len(first.Nodes) || len(g.Edges) != len(first.Edges) {
			t.Fatalf("run %d: %d/%d nodes/edges, want %d/%d",
				i, len(g.Nodes), len(g.Edges), len(first.Nodes), len(first.Edges))
		}
		for j, e := range g.Edges {
			if e != first.Edges[j] {
				t.Fatalf("run %d: edge %d = %+v, want %+v", i, j, e, first.Edges[j])
			}
		}
	}
}
