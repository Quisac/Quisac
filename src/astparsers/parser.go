package astparsers

import (
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

type Extracted struct {
	Imports             []string
	Functions           []string
	FunctionCalls       []string
	Classes             []string
	ClassInstantiations []string
}

type ASTParser interface {
	Parse(node *tree_sitter.Node, code []byte) *Extracted
}

var registry = map[string]ASTParser{
	".py": &PythonParser{},
	".js": &JavaScriptParser{},
	".ts": &JavaScriptParser{},
	".go": &GoParser{},
}

func Get(ext string) ASTParser {
	return registry[ext]
}
