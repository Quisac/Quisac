package core

import (
	"encoding/json"
	"strings"

	"quisac/internal/astparsers"
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

// Graph is an in-memory code graph. It is used both for a full repo index and
// for a single-file subgraph during incremental updates.
type Graph struct {
	Nodes map[string]*Node `json:"nodes"`
	Edges []Edge           `json:"edges"`

	// RawImports maps a file's repo-relative path to its raw import strings. It
	// feeds import resolution and is persisted so incremental updates can resolve
	// without re-parsing unchanged files.
	RawImports map[string][]string `json:"-"`

	// edgeSet deduplicates Edges in O(1) as they are added.
	edgeSet map[string]struct{}
}

func NewGraph() *Graph {
	return &Graph{
		Nodes:      make(map[string]*Node),
		Edges:      make([]Edge, 0),
		RawImports: make(map[string][]string),
		edgeSet:    make(map[string]struct{}),
	}
}

func (g *Graph) addNode(id, typ, name string) {
	if _, exists := g.Nodes[id]; !exists {
		g.Nodes[id] = &Node{ID: id, Type: typ, Name: name}
	}
}

func (g *Graph) addEdge(from, to, typ string) {
	key := from + "\x00" + typ + "\x00" + to
	if _, exists := g.edgeSet[key]; exists {
		return
	}
	g.edgeSet[key] = struct{}{}
	g.Edges = append(g.Edges, Edge{From: from, To: to, Type: typ})
}

func fileID(path string) string              { return "file:" + path }
func classID(path, name string) string       { return "class:" + path + "::" + name }
func funcID(path, name string) string        { return "func:" + path + "::" + name }
func methodID(path, qualified string) string { return "method:" + path + "::" + qualified }
func moduleID(name string) string            { return "module:" + name }
func varID(path, name string) string         { return "var:" + path + "::" + name }

// AddFile adds a parsed file's symbols and intra-file edges to the graph.
// Cross-file import edges are added separately via AddImportEdge.
func (g *Graph) AddFile(e *astparsers.Extracted) {
	fp := e.FilePath

	g.addNode(fileID(fp), "File", fp)

	if len(e.Imports) > 0 {
		g.RawImports[fp] = e.Imports
	}

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

	// Calls are attributed to their enclosing function/method, not the file.
	for _, c := range e.Calls {
		fromID := g.scopeToID(fp, c.CallerScope)
		switch c.Kind {
		case "method_call":
			toID := methodID(fp, c.Target)
			if g.Nodes[toID] == nil {
				g.addNode(moduleID(c.Target), "Module", c.Target)
				toID = moduleID(c.Target)
			}
			g.addEdge(fromID, toID, "Calls")
		case "instantiation":
			g.addEdge(fromID, resolveCall(fp, c.Target, g), "Instantiates")
		default:
			g.addEdge(fromID, resolveCall(fp, c.Target, g), "Calls")
		}
	}

	for _, d := range e.DecoratorUses {
		g.addNode(moduleID(d.Name), "Module", d.Name)
		g.addEdge(moduleID(d.Name), g.decoratorTargetID(fp, d.TargetScope), "Decorates")
	}

	for _, v := range e.GlobalVariables {
		g.addNode(varID(fp, v), "Variable", v)
		g.addEdge(fileID(fp), varID(fp, v), "Contains")
	}
}

// AddImportEdge adds a resolved file→file import edge when the target exists.
func (g *Graph) AddImportEdge(fromFile, toFile string) {
	if _, ok := g.Nodes[fileID(toFile)]; !ok {
		return
	}
	g.addEdge(fileID(fromFile), fileID(toFile), "Imports")
}

// PathFromID returns the repo-relative file path a node belongs to. IDs are
// shaped "file:PATH", "class:PATH::Name", etc.; "module:Name" returns "".
func PathFromID(id string) string {
	i := strings.IndexByte(id, ':')
	if i < 0 {
		return ""
	}
	prefix, rest := id[:i], id[i+1:]
	switch prefix {
	case "module":
		return ""
	case "file":
		return rest
	default:
		if j := strings.Index(rest, "::"); j >= 0 {
			return rest[:j]
		}
		return rest
	}
}

func (g *Graph) scopeToID(filePath, scope string) string {
	switch {
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
	if strings.HasPrefix(imp, "from ") || strings.HasPrefix(imp, "import ") {
		if parts := strings.Fields(imp); len(parts) >= 2 {
			return parts[1]
		}
	}
	return imp
}

func (g *Graph) ToJSON() (string, error) {
	b, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
