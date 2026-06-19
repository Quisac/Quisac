package core

import (
	"encoding/json"
	"fmt"
)

type Node struct {
	ID   string `json:"id"`
	Type string `json:"type"` // "File", "Class", "Function" , "Services"
	Name string `json:"name"`
}

type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"` // "Imports", "Contains", "Calls"."Instantiate"
}

type Graph struct {
	Nodes map[string]*Node `json:"nodes"`
	Edges []Edge           `json:"edges"`
}

func NewGraph() *Graph {
	return &Graph{
		Nodes: make(map[string]*Node),
		Edges: make([]Edge, 0),
	}
}

func (g *Graph) AddNode(id, typ, name string) {
	if _, exists := g.Nodes[id]; !exists {
		g.Nodes[id] = &Node{ID: id, Type: typ, Name: name}
	}
}

func (g *Graph) AddEdge(from, to, typ string) {
	g.Edges = append(g.Edges, Edge{From: from, To: to, Type: typ})
}

func (g *Graph) ToJSON() (string, error) {
	bytes, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

func (g *Graph) Print() {
	fmt.Printf("Graph Summary: %d nodes, %d edges\n\n", len(g.Nodes), len(g.Edges))

	fmt.Println("Nodes:")
	for _, node := range g.Nodes {
		fmt.Printf("  [%s] %s (%s)\n",
			node.ID,
			node.Name,
			node.Type,
		)
	}

	fmt.Println("\nEdges:")
	for _, edge := range g.Edges {
		fmt.Printf("  %s --[%s]--> %s\n",
			edge.From,
			edge.Type,
			edge.To,
		)
	}
}
