package main

import (
	"log"
	"net/http"
	"os"

	"quisac/src/github"

	"github.com/joho/godotenv"
)

const defaultPort = "8000"

func main() {
	_ = godotenv.Load()

	cfg, err := github.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	mux := http.NewServeMux()

	svc := github.NewService(cfg)
	handler := github.NewHandler(svc)

	handler.Register(mux)

	fs := http.FileServer(http.Dir("html"))
	mux.Handle("/", fs)

	addr := ":" + port

	log.Printf("listening on %s", addr)

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
