package astparsers

import tree_sitter "github.com/tree-sitter/go-tree-sitter"

type CallEdge struct {
	CallerScope string
	Target      string
	Kind        string
}

type DecoratorEdge struct {
	Name        string
	TargetScope string
}

type Extracted struct {
	FilePath        string
	Imports         []string
	Functions       []string   // top-level function names
	Methods         []string   // "ClassName.methodName"
	Classes         []string   // class names
	Inheritance     []string   // "Child->Parent"
	GlobalVariables []string   // module-level variable names
	Calls           []CallEdge // all call sites with caller context
	DecoratorUses   []DecoratorEdge
}

type ASTParser interface {
	Parse(node *tree_sitter.Node, code []byte, filePath string) *Extracted
}

var registry = map[string]ASTParser{
	".py":  &PythonParser{},
	".js":  &JavaScriptParser{},
	".jsx": &JavaScriptParser{},
	".ts":  &JavaScriptParser{},
	".tsx": &JavaScriptParser{},
	".go":  &GoParser{},
}

func Get(ext string) ASTParser {
	return registry[ext]
}
