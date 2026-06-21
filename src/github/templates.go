package github

import (
	"embed"
	"fmt"
	"html/template"
)

//go:embed templates/*.html
var templateFS embed.FS

// templateFuncs provides small helpers used by the templates.
var templateFuncs = template.FuncMap{
	// dict builds a map for passing multiple named values into a
	// sub-template invocation, e.g.
	//   {{template "github-mark" (dict "Size" 20 "Fill" "white")}}
	"dict": func(pairs ...any) (map[string]any, error) {
		if len(pairs)%2 != 0 {
			return nil, fmt.Errorf("dict: odd number of arguments")
		}
		m := make(map[string]any, len(pairs)/2)
		for i := 0; i < len(pairs); i += 2 {
			key, ok := pairs[i].(string)
			if !ok {
				return nil, fmt.Errorf("dict: key %v is not a string", pairs[i])
			}
			m[key] = pairs[i+1]
		}
		return m, nil
	},
}

// parseFlowPage parses the shared layout + the github mark partial + a
// page-specific body template (e.g. "connect.html") into one template set.
func parseFlowPage(bodyFile string) *template.Template {
	return template.Must(template.New("layout").Funcs(templateFuncs).ParseFS(
		templateFS,
		"templates/layout.html",
		"templates/github_mark.html",
		"templates/"+bodyFile,
	))
}

var (
	connectTmpl = parseFlowPage("connect.html")
	importTmpl  = parseFlowPage("import.html")
)
