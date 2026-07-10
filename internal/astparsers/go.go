package astparsers

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

type GoParser struct{}

func (p *GoParser) Parse(node *tree_sitter.Node, code []byte, filePath string) *Extracted {
	result := &Extracted{FilePath: filePath}
	goWalk(node, code, result, "", "file")
	return result
}

func goWalk(node *tree_sitter.Node, code []byte, result *Extracted, currentType, currentScope string) {
	switch node.Kind() {

	case "import_declaration":
		for i := range node.ChildCount() {
			child := node.Child(i)
			switch child.Kind() {
			case "import_spec":
				if p := importSpecPath(code, child); p != "" {
					result.Imports = append(result.Imports, p)
				}
			case "import_spec_list":
				for j := range child.ChildCount() {
					spec := child.Child(j)
					if spec.Kind() == "import_spec" {
						if p := importSpecPath(code, spec); p != "" {
							result.Imports = append(result.Imports, p)
						}
					}
				}
			}
		}
		return

	case "type_declaration":
		for i := range node.ChildCount() {
			spec := node.Child(i)
			if spec.Kind() != "type_spec" {
				continue
			}
			nameNode := spec.ChildByFieldName("name")
			if nameNode == nil {
				continue
			}
			typeName := nodeText(code, nameNode)
			result.Classes = append(result.Classes, typeName)

			// Interface embedding → Inheritance edges
			typeVal := spec.ChildByFieldName("type")
			if typeVal != nil && typeVal.Kind() == "interface_type" {
				for j := range typeVal.ChildCount() {
					member := typeVal.Child(j)
					// Embedded interface names appear as type_identifier children
					if member.Kind() == "type_identifier" {
						result.Inheritance = append(result.Inheritance,
							typeName+"->"+nodeText(code, member))
					}
				}
			}
		}
		return
	case "function_declaration":
		nameNode := node.ChildByFieldName("name")
		if nameNode == nil {
			break
		}
		fnName := nodeText(code, nameNode)
		result.Functions = append(result.Functions, fnName)
		newScope := "func:" + fnName

		if body := node.ChildByFieldName("body"); body != nil {
			for i := range body.ChildCount() {
				goWalk(body.Child(i), code, result, "", newScope)
			}
		}
		return

	case "method_declaration":
		receiverType := goReceiverType(code, node)
		nameNode := node.ChildByFieldName("name")
		if nameNode == nil {
			break
		}
		methodName := nodeText(code, nameNode)

		var newScope string
		if receiverType != "" {
			qualified := receiverType + "." + methodName
			result.Methods = append(result.Methods, qualified)
			newScope = "method:" + qualified
		} else {
			result.Functions = append(result.Functions, methodName)
			newScope = "func:" + methodName
		}

		if body := node.ChildByFieldName("body"); body != nil {
			for i := range body.ChildCount() {
				goWalk(body.Child(i), code, result, receiverType, newScope)
			}
		}
		return
	case "call_expression":
		funcNode := node.ChildByFieldName("function")
		if funcNode == nil {
			break
		}
		callName := nodeText(code, funcNode)
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
	case "composite_literal":
		typeNode := node.ChildByFieldName("type")
		if typeNode == nil {
			break
		}
		typeName := ""
		switch typeNode.Kind() {
		case "type_identifier":
			typeName = nodeText(code, typeNode)
		case "qualified_type": // pkg.Type
			typeName = nodeText(code, typeNode)
		}
		if typeName != "" {
			result.Calls = append(result.Calls, CallEdge{
				CallerScope: currentScope,
				Target:      typeName,
				Kind:        "instantiation",
			})
		}
	case "var_declaration", "const_declaration":
		if currentScope != "file" {
			break
		}
		for i := range node.ChildCount() {
			spec := node.Child(i)
			if spec.Kind() != "var_spec" && spec.Kind() != "const_spec" {
				continue
			}
			for j := range spec.ChildCount() {
				child := spec.Child(j)
				if child.Kind() == "identifier" {
					result.GlobalVariables = append(result.GlobalVariables,
						nodeText(code, child))
				}
			}
		}
	}
	for i := range node.ChildCount() {
		goWalk(node.Child(i), code, result, currentType, currentScope)
	}
}

func importSpecPath(code []byte, spec *tree_sitter.Node) string {
	pathNode := spec.ChildByFieldName("path")
	if pathNode == nil {
		return ""
	}
	// Check for blank/dot alias
	if nameNode := spec.ChildByFieldName("name"); nameNode != nil {
		alias := nodeText(code, nameNode)
		if alias == "_" || alias == "." {
			return ""
		}
	}
	raw := nodeText(code, pathNode)
	return strings.Trim(raw, `"`)
}

func goReceiverType(code []byte, methodNode *tree_sitter.Node) string {
	receiver := methodNode.ChildByFieldName("receiver")
	if receiver == nil {
		return ""
	}
	for i := range receiver.ChildCount() {
		param := receiver.Child(i)
		if param.Kind() != "parameter_declaration" {
			continue
		}
		typeNode := param.ChildByFieldName("type")
		if typeNode == nil {
			continue
		}
		switch typeNode.Kind() {
		case "type_identifier":
			return nodeText(code, typeNode)
		case "pointer_type":
			for j := range typeNode.ChildCount() {
				child := typeNode.Child(j)
				if child.Kind() == "type_identifier" {
					return nodeText(code, child)
				}
			}
		}
	}
	return ""
}
