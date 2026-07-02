package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"quisac/internal/config"
	"quisac/internal/db"
	"quisac/internal/filewalk"
	"quisac/internal/github"
	"quisac/internal/graphstore"
	"quisac/internal/ingest"
	"quisac/internal/server"
)

func main() {
	dir := flag.String("dir", "", "Index a local repository directory to html/graph.json")
	serve := flag.Bool("serve", false, "Start the HTTP server")
	flag.Parse()

	switch {
	case *serve:
		runServer()
	case *dir != "":
		runDirIndex(*dir)
	default:
		fmt.Println("usage: quisac --serve | --dir <path>")
		os.Exit(1)
	}
}

func runServer() {
	cfg, err := config.Load()
	if err != nil {
		exit("config", err)
	}

	ctx := context.Background()

	store, err := db.Open(ctx, cfg.Database.URL)
	if err != nil {
		exit("database", err)
	}
	defer store.Close()

	graphStore, err := graphstore.Open(ctx, cfg.Neo4j.URI, cfg.Neo4j.User, cfg.Neo4j.Password)
	if err != nil {
		exit("graph store", err)
	}
	defer graphStore.Close(ctx)

	app, err := github.New(cfg.GitHub)
	if err != nil {
		exit("github", err)
	}

	srv := server.New(cfg, store, app, ingest.New(app, graphStore))
	if err := srv.ListenAndServe(); err != nil {
		exit("server", err)
	}
}

func runDirIndex(dir string) {
	json, err := filewalk.ReadRepo(dir).ToJSON()
	if err != nil {
		exit("json", err)
	}
	if err := os.WriteFile("html/graph.json", []byte(json), 0o644); err != nil {
		exit("write", err)
	}
	fmt.Println("graph.json written to html/")
}

func exit(what string, err error) {
	fmt.Printf("%s error: %v\n", what, err)
	os.Exit(1)
}
