// For prototyping stage let's support only python js and golang
package filewalk

import (
	"fmt"
	"os"
	"path/filepath"

	"quisac/src/core"
	"quisac/src/treesitter"
)

func ReadRepo(dir string) *core.Graph {
	graph := core.NewGraph()

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "node_modules" || name == "__pycache__" || name == "go.mod" || name == "go.sum" {
				return filepath.SkipDir
			}
			return nil
		}

		ext := filepath.Ext(path)
		if ext == ".js" || ext == ".jsx" || ext == ".ts" || ext == ".tsx" || ext == ".py" || ext == ".go" || ext == ".cpp" {
			content, err := os.ReadFile(path)
			if err != nil {
				fmt.Printf("Error reading %s: %v\n", path, err)
				return nil
			}
			treesitter.ParseCode(content, ext)
		}

		return nil
	})

	if err != nil {
		fmt.Printf("Error walking the path %v: %v\n", dir, err)
	}

	return graph
}
