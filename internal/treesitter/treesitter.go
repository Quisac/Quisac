package treesitter

import (
	"errors"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	"quisac/internal/astparsers"
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

// Session holds one tree-sitter parser per language, reused across files, since
// loading a grammar per file is pure overhead over thousands of them. A parser
// carries the last parse's state, so a Session is not safe for concurrent use.
type Session struct {
	parsers map[string]*tree_sitter.Parser
}

func NewSession() *Session {
	return &Session{parsers: map[string]*tree_sitter.Parser{}}
}

// Close releases every parser the session built.
func (s *Session) Close() {
	for _, p := range s.parsers {
		p.Close()
	}
	clear(s.parsers)
}

// Parse extracts a file's symbols. filePath is used as the node ID in the graph.
func (s *Session) Parse(code []byte, ext string, filePath string) (*astparsers.Extracted, error) {
	astParser := astparsers.Get(ext)
	if astParser == nil {
		return nil, errors.New("no AST parser for extension: " + ext)
	}

	parser, ok := s.parsers[ext]
	if !ok {
		lang, err := getLanguage(ext)
		if err != nil {
			return nil, err
		}
		parser = tree_sitter.NewParser()
		if err := parser.SetLanguage(lang); err != nil {
			parser.Close()
			return nil, err
		}
		s.parsers[ext] = parser
	}

	tree := parser.Parse(code, nil)
	defer tree.Close()

	return astParser.Parse(tree.RootNode(), code, filePath), nil
}

// ParseCode parses a single file with a throwaway session. Callers parsing many
// files should build a Session and reuse it.
func ParseCode(code []byte, ext string, filePath string) (*astparsers.Extracted, error) {
	s := NewSession()
	defer s.Close()
	return s.Parse(code, ext, filePath)
}
