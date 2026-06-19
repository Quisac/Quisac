package astparsers

import (
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

type JavaScriptParser struct{}

func (p *JavaScriptParser) Parse(node *tree_sitter.Node, code []byte) *Extracted {
	result := &Extracted{}
	jsWalk(node, code, result)
	return result
}

func jsWalk(node *tree_sitter.Node, code []byte, result *Extracted) {
	switch node.Kind() {
	case "import_declaration":
		result.Imports = append(result.Imports, nodeText(code, node))

	case "call_expression":
		if n := node.ChildByFieldName("function"); n != nil {
			if nodeText(code, n) == "require" {
				result.Imports = append(result.Imports, nodeText(code, node))
				break
			}
		}

		if n := node.ChildByFieldName("function"); n != nil {
			callName := nodeText(code, n)
			leaf := lastSegment(callName)
			if len(leaf) > 0 && leaf[0] >= 'A' && leaf[0] <= 'Z' {
				result.ClassInstantiations = append(result.ClassInstantiations, callName)
			} else {
				result.FunctionCalls = append(result.FunctionCalls, callName)
			}
		}

	case "function_declaration":
		if n := node.ChildByFieldName("name"); n != nil {
			result.Functions = append(result.Functions, nodeText(code, n))
		}

	case "variable_declarator":
		valNode := node.ChildByFieldName("value")
		if valNode != nil &&
			(valNode.Kind() == "arrow_function" || valNode.Kind() == "function") {
			if n := node.ChildByFieldName("name"); n != nil {
				result.Functions = append(result.Functions, nodeText(code, n))
			}
		}

	case "class_declaration":
		if n := node.ChildByFieldName("name"); n != nil {
			result.Classes = append(result.Classes, nodeText(code, n))
		}

	case "new_expression":
		if n := node.ChildByFieldName("constructor"); n != nil {
			result.ClassInstantiations = append(result.ClassInstantiations, nodeText(code, n))
		}
	}

	for i := range node.ChildCount() {
		jsWalk(node.Child(i), code, result)
	}
}
