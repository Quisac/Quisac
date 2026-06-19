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
		filewalk.ReadRepo(*dir)
		return
	}

	if *repoURL != "" {
		return
	}

	fmt.Println("No mode selected.")
	fmt.Println("Use one of:")
	fmt.Println("  --serve")
	fmt.Println("  --dir <path>")
	fmt.Println("  --repo-url <url>")
	os.Exit(1)
}
