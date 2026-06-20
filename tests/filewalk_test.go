package tests

import (
	"os"
	"path/filepath"
	"quisac/src/filewalk"
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

	// 4. Verify that the import resolution worked and created an edge
	hasImportEdge := false
	for _, edge := range g.Edges {
		if edge.Type == "Imports" && edge.From == "file:"+pyFile && edge.To == "file:"+utilsFile {
			hasImportEdge = true
			break
		}
	}

	if !hasImportEdge {
		t.Errorf("Expected an Imports edge from main.py to utils.py, but it was not found")
	}
}
