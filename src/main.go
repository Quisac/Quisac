package main

import (
	"fmt"
	"net/http"
	"os"
)

const PORT = "8000"

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = PORT
	}

	fs := http.FileServer(http.Dir("html"))
	http.Handle("/", fs)
	fmt.Printf("serving → http://localhost:%s\n", port)
	http.ListenAndServe(":"+port, nil)
}
