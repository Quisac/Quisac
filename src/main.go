package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"quisac/src/github"

	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file, using environment")
	}

	cfg := github.ConfigFromEnv()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("config: %v", err)
	}

	mux := http.NewServeMux()
	github.Register(mux, cfg)
	mux.Handle("/", http.FileServer(http.Dir("html")))

	addr := "0.0.0.0:" + port()
	fmt.Printf("serving → http://localhost:%s\n", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func port() string {
	if p := os.Getenv("PORT"); p != "" {
		return p
	}
	return "8000"
}
