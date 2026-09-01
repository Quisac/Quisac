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

// External references — an npm package, the standard library, a symbol defined
// in another file — must not become nodes. They used to, and they made up the
// majority of the graph while being unreachable by any traversal.
func TestExternalReferencesAreNotNodes(t *testing.T) {
	g := core.NewGraph()
	g.AddFile(&astparsers.Extracted{
		FilePath:    "svc.py",
		Imports:     []string{"import os", "from django.db import models"},
		Functions:   []string{"handle"},
		Classes:     []string{"Service"},
		Inheritance: []string{"Service->models.Model"}, // parent is external
		Calls: []astparsers.CallEdge{
			{CallerScope: "func:handle", Target: "os.getenv", Kind: "call"},        // external
			{CallerScope: "func:handle", Target: "Service", Kind: "instantiation"}, // local
		},
		DecoratorUses: []astparsers.DecoratorEdge{{Name: "app.route", TargetScope: "func:handle"}},
	})

	allowed := map[string]bool{"File": true, "Class": true, "Function": true, "Method": true, "Variable": true}
	for id, n := range g.Nodes {
		if !allowed[n.Type] {
			t.Errorf("node %q has synthetic type %q", id, n.Type)
		}
	}

	// The local instantiation still connects; the external call does not.
	if !hasEdge(g, "func:svc.py::handle", "class:svc.py::Service", "Instantiates") {
		t.Error("local instantiation edge missing")
	}
	for _, e := range g.Edges {
		if e.To == "module:os.getenv" || e.To == "module:models.Model" {
			t.Errorf("edge to external reference survived: %+v", e)
		}
	}

	// The imports themselves are still recorded, just not as nodes.
	if got := len(g.RawImports["svc.py"]); got != 2 {
		t.Errorf("RawImports = %d entries, want 2", got)
	}
}

// Every edge endpoint must exist as a node. Dangling edges are what a graph
// renderer chokes on, and dropping external nodes is exactly the change that
// could introduce them.
func TestNoDanglingEdges(t *testing.T) {
	g := core.NewGraph()
	g.AddFile(&astparsers.Extracted{
		FilePath:        "app.js",
		Imports:         []string{"import React from 'react'"},
		Functions:       []string{"render"},
		Classes:         []string{"Widget"},
		Methods:         []string{"Widget.draw"},
		GlobalVariables: []string{"config"},
		Inheritance:     []string{"Widget->React.Component"},
		Calls: []astparsers.CallEdge{
			{CallerScope: "method:Widget.draw", Target: "render", Kind: "call"},
			{CallerScope: "method:Widget.draw", Target: "fetch", Kind: "call"},
			{CallerScope: "func:render", Target: "Widget", Kind: "instantiation"},
			{CallerScope: "func:render", Target: "Date", Kind: "instantiation"},
		},
	})

	for _, e := range g.Edges {
		if g.Nodes[e.From] == nil {
			t.Errorf("edge %+v has no source node", e)
		}
		if g.Nodes[e.To] == nil {
			t.Errorf("edge %+v has no target node", e)
		}
	}
}

func hasEdge(g *core.Graph, from, to, typ string) bool {
	for _, e := range g.Edges {
		if e.From == from && e.To == to && e.Type == typ {
			return true
		}
	}
	return false
}
