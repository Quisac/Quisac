package astparsers

import (
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

type GoParser struct{}

func (p *GoParser) Parse(node *tree_sitter.Node, code []byte, filePath string) *Extracted {
	result := &Extracted{FilePath: filePath}
	goWalk(node, code, result, "file")
	return result
}

func goWalk(node *tree_sitter.Node, code []byte, result *Extracted, currentScope string) {
	switch node.Kind() {

	case "import_declaration":
		result.Imports = append(result.Imports, nodeText(code, node))

	case "function_declaration", "method_declaration":
		if n := node.ChildByFieldName("name"); n != nil {
			fnName := nodeText(code, n)
			result.Functions = append(result.Functions, fnName)
			newScope := "func:" + fnName
			if body := node.ChildByFieldName("body"); body != nil {
				for i := range body.ChildCount() {
					goWalk(body.Child(i), code, result, newScope)
				}
			}
			return
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
			kind := "call"
			if len(leaf) > 0 && leaf[0] >= 'A' && leaf[0] <= 'Z' {
				kind = "instantiation"
			}
			result.Calls = append(result.Calls, CallEdge{
				CallerScope: currentScope,
				Target:      callName,
				Kind:        kind,
			})
		}
	}

	for i := range node.ChildCount() {
		goWalk(node.Child(i), code, result, currentScope)
	}
}
