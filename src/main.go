package main

import (
	"fmt"
	"net/http"
)

const PORT = "8000"

func main() {
	fs := http.FileServer(http.Dir("html"))
	http.Handle("/", fs)
	fmt.Printf("serving → http://localhost:%s\n", PORT)
	http.ListenAndServe(":"+PORT, nil)
}
