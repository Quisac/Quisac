package astparsers

import tree_sitter "github.com/tree-sitter/go-tree-sitter"

func nodeText(code []byte, node *tree_sitter.Node) string {
	return string(code[node.StartByte():node.EndByte()])
}

func lastSegment(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			return name[i+1:]
		}
	}
	return name
}
