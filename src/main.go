package main

import (
	"fmt"
	"net/http"
	"os"
)

const DEFAULT_PORT = "8000"

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = DEFAULT_PORT
	}

	fs := http.FileServer(http.Dir("html"))
	http.Handle("/", fs)
	fmt.Printf("serving → http://localhost:%s\n", port)
	http.ListenAndServe("0.0.0.0:"+port, nil)
}
