package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"quisac/src/github"

	"github.com/joho/godotenv"
)

const DEFAULT_PORT = "8000"

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found, using environment variables")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = DEFAULT_PORT
	}

	cfg := github.ConfigFromEnv()

	if cfg.AppSlug == "" || cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.RedirectURL == "" {
		log.Fatalf("missing GitHub config — check your .env:\n  GITHUB_APP_SLUG=%q\n  GITHUB_CLIENT_ID=%q\n  GITHUB_CLIENT_SECRET=%q\n  GITHUB_REDIRECT_URL=%q",
			cfg.AppSlug, cfg.ClientID, cfg.ClientSecret, cfg.RedirectURL)
	}

	github.Handler(http.DefaultServeMux, cfg)

	// Serve frontend from html/
	fs := http.FileServer(http.Dir("html"))
	http.Handle("/", fs)

	fmt.Printf("serving → http://localhost:%s\n", port)
	if err := http.ListenAndServe("0.0.0.0:"+port, nil); err != nil {
		log.Fatal(err)
	}
}
