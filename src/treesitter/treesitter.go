package treesitter

import (
	"errors"
	"fmt"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	"quisac/src/astparsers"
)

// PythonExtracted holds all extracted Python constructs
type PythonExtracted struct {
	Imports             []string
	Functions           []string
	FunctionCalls       []string
	Classes             []string
	ClassInstantiations []string
}

func getLanguage(ext string) (*tree_sitter.Language, error) {
	switch ext {
	case ".js", ".ts":
		return tree_sitter.NewLanguage(tree_sitter_javascript.Language()), nil
	case ".py":
		return tree_sitter.NewLanguage(tree_sitter_python.Language()), nil
	case ".go":
		return tree_sitter.NewLanguage(tree_sitter_go.Language()), nil
	default:
		return nil, errors.New("unsupported language: " + ext)
	}
}

func ParseCode(code []byte, ext string) error {
	parser := tree_sitter.NewParser()
	defer parser.Close()

	lang, err := getLanguage(ext)
	if err != nil {
		return err
	}
	parser.SetLanguage(lang)

	tree := parser.Parse(code, nil)
	defer tree.Close()

	//root := tree.RootNode()
	//fmt.Println(root.ToSexp())
	astParser := astparsers.Get(ext)
	if astParser == nil {
		return errors.New("no AST parser for extension: " + ext)
	}

	extracted := astParser.Parse(tree.RootNode(), code)

	fmt.Println("Imports:             ", extracted.Imports)
	fmt.Println("Functions:           ", extracted.Functions)
	fmt.Println("Classes:             ", extracted.Classes)
	fmt.Println("Function Calls:      ", extracted.FunctionCalls)
	fmt.Println("Class Instantiations:", extracted.ClassInstantiations)
	return nil
}
