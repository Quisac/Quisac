package core

import (
	"encoding/json"
	"fmt"
	"quisac/src/astparsers"
	"strings"
)

type Node struct {
	ID   string `json:"id"`
	Type string `json:"type"` // File | Class | Function | Method | Module | Variable
	Name string `json:"name"`
}

type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"` // Imports | Contains | Calls | Instantiates | Inherits | Decorates
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

func (g *Graph) addNode(id, typ, name string) {
	if _, exists := g.Nodes[id]; !exists {
		g.Nodes[id] = &Node{ID: id, Type: typ, Name: name}
	}
}

func (g *Graph) addEdge(from, to, typ string) {
	for _, e := range g.Edges {
		if e.From == from && e.To == to && e.Type == typ {
			return
		}
	}
	g.Edges = append(g.Edges, Edge{From: from, To: to, Type: typ})
}

func fileID(path string) string              { return "file:" + path }
func classID(path, name string) string       { return "class:" + path + "::" + name }
func funcID(path, name string) string        { return "func:" + path + "::" + name }
func methodID(path, qualified string) string { return "method:" + path + "::" + qualified }
func moduleID(name string) string            { return "module:" + name }
func varID(path, name string) string         { return "var:" + path + "::" + name }

func (g *Graph) AddFile(e *astparsers.Extracted) {
	fp := e.FilePath

	g.addNode(fileID(fp), "File", fp)

	for _, imp := range e.Imports {
		modName := firstToken(imp)
		g.addNode(moduleID(modName), "Module", modName)
		g.addEdge(fileID(fp), moduleID(modName), "Imports")
	}

	for _, cls := range e.Classes {
		g.addNode(classID(fp, cls), "Class", cls)
		g.addEdge(fileID(fp), classID(fp, cls), "Contains")
	}

	for _, inh := range e.Inheritance {
		parts := strings.SplitN(inh, "->", 2)
		if len(parts) != 2 {
			continue
		}
		child, parent := parts[0], parts[1]
		childID := classID(fp, child)
		// Parent might be external
		parentID := classID(fp, parent)
		if _, ok := g.Nodes[parentID]; !ok {
			g.addNode(moduleID(parent), "Module", parent)
			parentID = moduleID(parent)
		}
		g.addEdge(childID, parentID, "Inherits")
	}

	for _, fn := range e.Functions {
		g.addNode(funcID(fp, fn), "Function", fn)
		g.addEdge(fileID(fp), funcID(fp, fn), "Contains")
	}

	for _, m := range e.Methods {
		parts := strings.SplitN(m, ".", 2)
		if len(parts) != 2 {
			continue
		}
		cls, methodName := parts[0], parts[1]
		mID := methodID(fp, m)
		g.addNode(mID, "Method", methodName)
		g.addEdge(classID(fp, cls), mID, "Contains")
	}

	// Calls — attributed to their enclosing function/method, not the file.
	for _, c := range e.Calls {
		fromID := g.scopeToID(fp, c.CallerScope)
		var toID string
		switch c.Kind {
		case "method_call":
			toID = methodID(fp, c.Target)
			if g.Nodes[toID] == nil {
				g.addNode(moduleID(c.Target), "Module", c.Target)
				toID = moduleID(c.Target)
			}
			g.addEdge(fromID, toID, "Calls")
		case "instantiation":
			toID = resolveCall(fp, c.Target, g)
			g.addEdge(fromID, toID, "Instantiates")
		default:
			toID = resolveCall(fp, c.Target, g)
			g.addEdge(fromID, toID, "Calls")
		}
	}

	// Decorators — emit Decorates edge to the specific function/method/class.
	for _, d := range e.DecoratorUses {
		g.addNode(moduleID(d.Name), "Module", d.Name)
		toID := g.decoratorTargetID(fp, d.TargetScope)
		g.addEdge(moduleID(d.Name), toID, "Decorates")
	}

	for _, v := range e.GlobalVariables {
		g.addNode(varID(fp, v), "Variable", v)
		g.addEdge(fileID(fp), varID(fp, v), "Contains")
	}
}

func (g *Graph) AddImportEdge(fromFile, toFile string) {
	if _, ok := g.Nodes[fileID(toFile)]; !ok {
		return
	}
	g.addEdge(fileID(fromFile), fileID(toFile), "Imports")
}

func (g *Graph) scopeToID(filePath, scope string) string {
	switch {
	case scope == "file":
		return fileID(filePath)
	case strings.HasPrefix(scope, "func:"):
		return funcID(filePath, strings.TrimPrefix(scope, "func:"))
	case strings.HasPrefix(scope, "method:"):
		return methodID(filePath, strings.TrimPrefix(scope, "method:"))
	case strings.HasPrefix(scope, "class:"):
		return classID(filePath, strings.TrimPrefix(scope, "class:"))
	default:
		return fileID(filePath)
	}
}

func (g *Graph) decoratorTargetID(filePath, targetScope string) string {
	switch {
	case strings.HasPrefix(targetScope, "func:"):
		return funcID(filePath, strings.TrimPrefix(targetScope, "func:"))
	case strings.HasPrefix(targetScope, "method:"):
		return methodID(filePath, strings.TrimPrefix(targetScope, "method:"))
	case strings.HasPrefix(targetScope, "class:"):
		return classID(filePath, strings.TrimPrefix(targetScope, "class:"))
	default:
		return fileID(filePath)
	}
}

func resolveCall(filePath, name string, g *Graph) string {
	if id := funcID(filePath, name); g.Nodes[id] != nil {
		return id
	}
	if id := classID(filePath, name); g.Nodes[id] != nil {
		return id
	}
	g.addNode(moduleID(name), "Module", name)
	return moduleID(name)
}

func firstToken(imp string) string {
	imp = strings.TrimSpace(imp)
	if strings.HasPrefix(imp, "from ") {
		parts := strings.Fields(imp)
		if len(parts) >= 2 {
			return parts[1]
		}
	}
	if strings.HasPrefix(imp, "import ") {
		parts := strings.Fields(imp)
		if len(parts) >= 2 {
			return parts[1]
		}
	}
	return imp
}

func (g *Graph) ToJSON() (string, error) {
	bytes, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

func (g *Graph) Print() {
	fmt.Printf("Graph: %d nodes, %d edges\n\n", len(g.Nodes), len(g.Edges))
	fmt.Println("Nodes:")
	for _, node := range g.Nodes {
		fmt.Printf("  [%s] %s (%s)\n", node.Type, node.Name, node.ID)
	}
	fmt.Println("\nEdges:")
	for _, edge := range g.Edges {
		fmt.Printf("  %s --[%s]--> %s\n", edge.From, edge.Type, edge.To)
	}
}
