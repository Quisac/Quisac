package core

import (
	"encoding/json"
	"strings"

	"quisac/internal/astparsers"
)

type Node struct {
	ID   string `json:"id"`
	Type string `json:"type"` // File | Class | Function | Method | Variable
	Name string `json:"name"`
}

type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"` // Imports | Contains | Calls | Instantiates | Inherits
}

// Graph is an in-memory code graph, used both for a full repo index and for a
// single-file subgraph during incremental updates.
//
// Only symbols that exist in the repository become nodes. A name the parser
// cannot place inside the repo — an npm package, the standard library, a base
// class from a dependency — is recorded on the importing file in RawImports
// instead. Synthesising a node per unresolved name was 60% of the nodes and half
// the edges on this repository, and none of it could reach a traversal result:
// such a node has no outgoing edge, so no path runs through it to a failure.
type Graph struct {
	Nodes map[string]*Node `json:"nodes"`
	Edges []Edge           `json:"edges"`

	// RawImports maps a repo-relative path to its raw import strings. It feeds
	// import resolution, is persisted so incremental updates resolve without
	// re-parsing, and is how a viewer sees a file's external dependencies.
	RawImports map[string][]string `json:"raw_imports,omitempty"`

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
func varID(path, name string) string         { return "var:" + path + "::" + name }

// AddFile adds a parsed file's symbols and intra-file edges to the graph.
// Cross-file import edges are added separately via AddImportEdge.
func (g *Graph) AddFile(e *astparsers.Extracted) {
	fp := e.FilePath

	g.addNode(fileID(fp), "File", fp)

	if len(e.Imports) > 0 {
		g.RawImports[fp] = e.Imports
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
		parentID := classID(fp, parent)
		if _, ok := g.Nodes[parentID]; !ok {
			continue // parent lives outside this file — an external reference
		}
		g.addEdge(classID(fp, child), parentID, "Inherits")
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
			if toID := methodID(fp, c.Target); g.Nodes[toID] != nil {
				g.addEdge(fromID, toID, "Calls")
			}
		case "instantiation":
			if toID, ok := resolveCall(fp, c.Target, g); ok {
				g.addEdge(fromID, toID, "Instantiates")
			}
		default:
			if toID, ok := resolveCall(fp, c.Target, g); ok {
				g.addEdge(fromID, toID, "Calls")
			}
		}
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

// resolveCall maps a call target to a symbol declared in the same file. An
// unresolved target names something the file cannot see, and produces no edge.
func resolveCall(filePath, name string, g *Graph) (string, bool) {
	if id := funcID(filePath, name); g.Nodes[id] != nil {
		return id, true
	}
	if id := classID(filePath, name); g.Nodes[id] != nil {
		return id, true
	}
	return "", false
}

func (g *Graph) ToJSON() (string, error) {
	b, err := json.Marshal(g)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
