package astparsers

import (
	"fmt"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

type PythonParser struct{}

func (p *PythonParser) Parse(node *tree_sitter.Node, code []byte, filePath string) *Extracted {
	result := &Extracted{FilePath: filePath}
	pythonWalk(node, code, result, "", "file")
	return result
}

func pythonWalk(node *tree_sitter.Node, code []byte, result *Extracted, currentClass, currentScope string) {
	switch node.Kind() {

	case "import_statement", "import_from_statement":
		result.Imports = append(result.Imports, nodeText(code, node))
		return

	case "decorated_definition":
		var decNames []string
		var defNode *tree_sitter.Node

		for i := range node.ChildCount() {
			child := node.Child(i)
			switch child.Kind() {
			case "decorator":
				if n := decoratorName(code, child); n != "" {
					decNames = append(decNames, n)
				}
			case "function_definition", "async_function_definition", "class_definition":
				defNode = child
			}
		}

		if defNode == nil {
			return
		}

		if nameNode := defNode.ChildByFieldName("name"); nameNode != nil {
			defName := nodeText(code, nameNode)
			var targetScope string
			switch {
			case defNode.Kind() == "class_definition":
				targetScope = "class:" + defName
			case currentClass != "":
				targetScope = "method:" + currentClass + "." + defName
			default:
				targetScope = "func:" + defName
			}
			for _, dec := range decNames {
				result.DecoratorUses = append(result.DecoratorUses, DecoratorEdge{
					Name:        dec,
					TargetScope: targetScope,
				})
			}
		}

		pythonWalk(defNode, code, result, currentClass, currentScope)
		return

	case "class_definition":
		nameNode := node.ChildByFieldName("name")
		if nameNode == nil {
			break
		}
		className := nodeText(code, nameNode)
		result.Classes = append(result.Classes, className)

		if bases := node.ChildByFieldName("superclasses"); bases != nil {
			for i := range bases.ChildCount() {
				b := bases.Child(i)
				if b.Kind() == "identifier" || b.Kind() == "dotted_name" {
					result.Inheritance = append(result.Inheritance,
						fmt.Sprintf("%s->%s", className, nodeText(code, b)))
				}
			}
		}

		if body := node.ChildByFieldName("body"); body != nil {
			for i := range body.ChildCount() {
				pythonWalk(body.Child(i), code, result, className, currentScope)
			}
		}
		return

	case "function_definition", "async_function_definition":
		nameNode := node.ChildByFieldName("name")
		if nameNode == nil {
			break
		}
		fnName := nodeText(code, nameNode)

		var newScope string
		if currentClass != "" {
			result.Methods = append(result.Methods, fmt.Sprintf("%s.%s", currentClass, fnName))
			newScope = "method:" + currentClass + "." + fnName
		} else {
			result.Functions = append(result.Functions, fnName)
			newScope = "func:" + fnName
		}

		if body := node.ChildByFieldName("body"); body != nil {
			for i := range body.ChildCount() {
				pythonWalk(body.Child(i), code, result, currentClass, newScope)
			}
		}
		return

	case "call":
		funcNode := node.ChildByFieldName("function")
		if funcNode == nil {
			break
		}
		callName := nodeText(code, funcNode)

		if strings.HasPrefix(callName, "self.") && currentClass != "" {
			methodPart := strings.TrimPrefix(callName, "self.")
			if !strings.Contains(methodPart, ".") {
				result.Calls = append(result.Calls, CallEdge{
					CallerScope: currentScope,
					Target:      currentClass + "." + methodPart,
					Kind:        "method_call",
				})
			}
			break // don't fall through to the generic handler
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

	case "expression_statement":
		if currentScope == "file" {
			for i := range node.ChildCount() {
				child := node.Child(i)
				if child.Kind() == "assignment" {
					if target := child.ChildByFieldName("left"); target != nil {
						result.GlobalVariables = append(result.GlobalVariables,
							nodeText(code, target))
					}
				}
			}
		}
	}

	for i := range node.ChildCount() {
		pythonWalk(node.Child(i), code, result, currentClass, currentScope)
	}
}

func decoratorName(code []byte, decNode *tree_sitter.Node) string {
	for i := range decNode.ChildCount() {
		child := decNode.Child(i)
		if child.Kind() != "@" {
			return nodeText(code, child)
		}
	}
	return ""
}
