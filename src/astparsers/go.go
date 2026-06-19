package astparsers

import (
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

type GoParser struct{}

func (p *GoParser) Parse(node *tree_sitter.Node, code []byte) *Extracted {
	result := &Extracted{}
	goWalk(node, code, result)
	return result
}

func goWalk(node *tree_sitter.Node, code []byte, result *Extracted) {
	switch node.Kind() {

	case "import_declaration":
		result.Imports = append(result.Imports, nodeText(code, node))

	case "function_declaration", "method_declaration":
		if n := node.ChildByFieldName("name"); n != nil {
			result.Functions = append(result.Functions, nodeText(code, n))
		}

	case "type_declaration":
		for i := range node.ChildCount() {
			spec := node.Child(i)
			if spec.Kind() == "type_spec" {
				if n := spec.ChildByFieldName("name"); n != nil {
					result.Classes = append(result.Classes, nodeText(code, n))
				}
			}
		}

	case "call_expression":
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
		goWalk(node.Child(i), code, result)
	}
}
