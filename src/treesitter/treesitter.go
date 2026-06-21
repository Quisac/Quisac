package treesitter

import (
	"errors"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	"quisac/src/astparsers"
)

func getLanguage(ext string) (*tree_sitter.Language, error) {
	switch ext {
	case ".js", ".jsx", ".ts", ".tsx":
		return tree_sitter.NewLanguage(tree_sitter_javascript.Language()), nil
	case ".py":
		return tree_sitter.NewLanguage(tree_sitter_python.Language()), nil
	case ".go":
		return tree_sitter.NewLanguage(tree_sitter_go.Language()), nil
	default:
		return nil, errors.New("unsupported language: " + ext)
	}
}

// ParseCode parses a file and returns the extracted symbols.
// filePath is used as the node ID in the graph.
func ParseCode(code []byte, ext string, filePath string) (*astparsers.Extracted, error) {
	parser := tree_sitter.NewParser()
	defer parser.Close()

	lang, err := getLanguage(ext)
	if err != nil {
		return nil, err
	}
	parser.SetLanguage(lang)

	tree := parser.Parse(code, nil)
	defer tree.Close()

	astParser := astparsers.Get(ext)
	if astParser == nil {
		return nil, errors.New("no AST parser for extension: " + ext)
	}

	// Pass filePath so extracted result knows which file it came from
	extracted := astParser.Parse(tree.RootNode(), code, filePath)
	return extracted, nil
}
