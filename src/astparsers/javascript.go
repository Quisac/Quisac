package astparsers

import tree_sitter "github.com/tree-sitter/go-tree-sitter"

type JavaScriptParser struct{}

func (p *JavaScriptParser) Parse(node *tree_sitter.Node, code []byte, filePath string) *Extracted {
	result := &Extracted{FilePath: filePath}
	jsWalk(node, code, result, "", "file")
	return result
}

func jsWalk(node *tree_sitter.Node, code []byte, result *Extracted, currentClass, currentScope string) {
	switch node.Kind() {

	case "import_statement", "import_declaration":
		result.Imports = append(result.Imports, nodeText(code, node))
		return

	case "call_expression":
		funcNode := node.ChildByFieldName("function")
		if funcNode == nil {
			break
		}
		callName := nodeText(code, funcNode)

		if callName == "require" {
			result.Imports = append(result.Imports, nodeText(code, node))
			return
		}

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

	case "new_expression":
		if n := node.ChildByFieldName("constructor"); n != nil {
			result.Calls = append(result.Calls, CallEdge{
				CallerScope: currentScope,
				Target:      nodeText(code, n),
				Kind:        "instantiation",
			})
		}

	case "function_declaration":
		if n := node.ChildByFieldName("name"); n != nil {
			fnName := nodeText(code, n)
			var newScope string
			if currentClass != "" {
				result.Methods = append(result.Methods, currentClass+"."+fnName)
				newScope = "method:" + currentClass + "." + fnName
			} else {
				result.Functions = append(result.Functions, fnName)
				newScope = "func:" + fnName
			}
			if body := node.ChildByFieldName("body"); body != nil {
				for i := range body.ChildCount() {
					jsWalk(body.Child(i), code, result, currentClass, newScope)
				}
			}
			return
		}

	case "method_definition":
		if n := node.ChildByFieldName("name"); n != nil {
			methodName := nodeText(code, n)
			if currentClass != "" {
				result.Methods = append(result.Methods, currentClass+"."+methodName)
			}
			newScope := "method:" + currentClass + "." + methodName
			if body := node.ChildByFieldName("body"); body != nil {
				for i := range body.ChildCount() {
					jsWalk(body.Child(i), code, result, currentClass, newScope)
				}
			}
			return
		}

	case "variable_declarator":
		val := node.ChildByFieldName("value")
		if val != nil && (val.Kind() == "arrow_function" || val.Kind() == "function") {
			if n := node.ChildByFieldName("name"); n != nil {
				fnName := nodeText(code, n)
				result.Functions = append(result.Functions, fnName)
				newScope := "func:" + fnName
				if body := val.ChildByFieldName("body"); body != nil {
					for i := range body.ChildCount() {
						jsWalk(body.Child(i), code, result, currentClass, newScope)
					}
				}
				return
			}
		}

	case "class_declaration":
		if n := node.ChildByFieldName("name"); n != nil {
			className := nodeText(code, n)
			result.Classes = append(result.Classes, className)
			if body := node.ChildByFieldName("body"); body != nil {
				for i := range body.ChildCount() {
					jsWalk(body.Child(i), code, result, className, currentScope)
				}
			}
			return
		}

	case "decorator":
		for i := range node.ChildCount() {
			child := node.Child(i)
			if child.Kind() != "@" {
				// Best-effort: we don't always know the decorated target here,
				// so we record with an empty TargetScope and let graph.go fall
				// back to the file node.
				result.DecoratorUses = append(result.DecoratorUses, DecoratorEdge{
					Name:        nodeText(code, child),
					TargetScope: "",
				})
			}
		}
		return
	}

	for i := range node.ChildCount() {
		jsWalk(node.Child(i), code, result, currentClass, currentScope)
	}
}
