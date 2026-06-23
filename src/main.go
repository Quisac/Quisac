package main

import (
	"flag"
	"fmt"
	"os"

	"quisac/src/config"
	"quisac/src/filewalk"
	"quisac/src/server"
)

func main() {
	var (
		dir     = flag.String("dir", "", "Repository directory to index")
		serve   = flag.Bool("serve", false, "Start HTTP server")
		repoURL = flag.String("repo-url", "", "Remote repository URL")
	)
	flag.Parse()

	if *serve {
		cfg, err := config.Load()
		if err != nil {
			fmt.Println("config error:", err)
			os.Exit(1)
		}
		if err := server.New(cfg).ListenAndServe(); err != nil {
			fmt.Println("server error:", err)
			os.Exit(1)
		}
		return
	}

	if *dir != "" {
		graph := filewalk.ReadRepo(*dir)
		graph.Print()
		json, err := graph.ToJSON()
		if err != nil {
			fmt.Println("json error:", err)
			os.Exit(1)
		}
		if err := os.WriteFile("html/graph.json", []byte(json), 0o644); err != nil {
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
