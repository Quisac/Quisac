package astparsers

import (
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

type PythonParser struct{}

func (p *PythonParser) Parse(node *tree_sitter.Node, code []byte) *Extracted {
	result := &Extracted{}
	pythonWalk(node, code, result)
	return result
}

func pythonWalk(node *tree_sitter.Node, code []byte, result *Extracted) {
	switch node.Kind() {
	case "import_statement", "import_from_statement":
		result.Imports = append(result.Imports, nodeText(code, node))

	case "function_definition":
		if n := node.ChildByFieldName("name"); n != nil {
			result.Functions = append(result.Functions, nodeText(code, n))
		}

	case "class_definition":
		if n := node.ChildByFieldName("name"); n != nil {
			result.Classes = append(result.Classes, nodeText(code, n))
		}

	case "call":
		if n := node.ChildByFieldName("function"); n != nil {
			callName := nodeText(code, n)
			leaf := lastSegment(callName)
			if len(leaf) > 0 && leaf[0] >= 'A' && leaf[0] <= 'Z' {
				result.ClassInstantiations = append(result.ClassInstantiations, callName)
			} else {
				result.FunctionCalls = append(result.FunctionCalls, callName)
			}
		}
	}

	for i := range node.ChildCount() {
		pythonWalk(node.Child(i), code, result)
	}
}
