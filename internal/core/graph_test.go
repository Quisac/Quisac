package core_test

import (
	"encoding/json"
	"quisac/internal/astparsers"
	"quisac/internal/core"
	"testing"
)

func TestGraphCreation(t *testing.T) {
	g := core.NewGraph()
	if g == nil {
		t.Fatal("Expected new graph, got nil")
	}

	e := &astparsers.Extracted{
		FilePath:  "main.py",
		Imports:   []string{"import os"},
		Functions: []string{"main"},
		Classes:   []string{"App"},
		Calls: []astparsers.CallEdge{
			{CallerScope: "func:main", Target: "os.getenv", Kind: "call"},
		},
	}

	g.AddFile(e)

	if len(g.Nodes) == 0 {
		t.Errorf("Expected nodes to be added, got 0")
	}

	// Verify ToJSON works and produces valid JSON
	js, err := g.ToJSON()
	if err != nil {
		t.Fatalf("Error serializing to JSON: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(js), &parsed); err != nil {
		t.Errorf("Invalid JSON output: %v", err)
	}

	// Basic check on nodes map
	if _, exists := parsed["nodes"]; !exists {
		t.Errorf("JSON missing 'nodes' key")
	}
}
