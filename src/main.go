package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"quisac/src/filewalk"
)

const PORT = "8000"

func main() {
	var (
		dir     = flag.String("dir", "", "Repository directory to index")
		serve   = flag.Bool("serve", false, "Start HTTP server")
		repoURL = flag.String("repo-url", "", "Remote repository URL")
	)
	flag.Parse()

	if *serve {
		fs := http.FileServer(http.Dir("html"))
		http.Handle("/", fs)
		fmt.Printf("serving → http://localhost:%s\n", PORT)
		if err := http.ListenAndServe(":"+PORT, nil); err != nil {
			fmt.Println("server error:", err)
		}
		return
	}

	if *dir != "" {
		graph := filewalk.ReadRepo(*dir)
		graph.Print()

		// Also emit JSON for the frontend
		json, err := graph.ToJSON()
		if err != nil {
			fmt.Println("json error:", err)
			os.Exit(1)
		}
		if err := os.WriteFile("html/graph.json", []byte(json), 0644); err != nil {
			fmt.Println("write error:", err)
			os.Exit(1)
		}
		fmt.Println("\ngraph.json written to html/")
		return
	}

	if *repoURL != "" {
		fmt.Println("repo-url cloning not yet implemented")
		return
	}

	fmt.Println("No mode selected.")
	fmt.Println("  --serve")
	fmt.Println("  --dir <path>")
	fmt.Println("  --repo-url <url>")
	os.Exit(1)
}
