package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"quisac/internal/config"
	"quisac/internal/db"
	"quisac/internal/filewalk"
	"quisac/internal/github"
	"quisac/internal/graphstore"
	"quisac/internal/ingest"
	"quisac/internal/server"
	"quisac/internal/worker"
)

// shutdownGrace bounds how long we wait, per stage, for HTTP connections to
// drain and workers to finish in-flight jobs during a graceful shutdown.
const shutdownGrace = 30 * time.Second

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
	log := newLogger()
	slog.SetDefault(log)

	cfg, err := config.Load()
	if err != nil {
		exit("config", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := db.Open(ctx, cfg.Database.URL)
	if err != nil {
		exit("database", err)
	}
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		exit("migrate", err)
	}

	graphStore, err := graphstore.Open(ctx, cfg.Neo4j.URI, cfg.Neo4j.User, cfg.Neo4j.Password)
	if err != nil {
		exit("graph store", err)
	}
	defer graphStore.Close(context.Background())

	app, err := github.New(cfg.GitHub)
	if err != nil {
		exit("github", err)
	}

	pool := worker.New(store, ingest.New(app, graphStore, log), log, worker.Config{})
	srv := server.New(cfg, log, store, store, pool, app)
	srv.AddReadinessCheck("postgres", store.Ping)
	srv.AddReadinessCheck("neo4j", graphStore.Ping)

	httpSrv := &http.Server{Addr: ":" + cfg.Server.Port, Handler: srv.Handler()}

	poolDone := make(chan struct{})
	go func() {
		pool.Run(ctx)
		close(poolDone)
	}()

	go func() {
		log.Info("serving", "addr", httpSrv.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server", "err", err)
			stop() // unblock the main goroutine so deferred cleanup runs
		}
	}()

	<-ctx.Done()
	log.Info("shutdown signal received; draining")

	// Stop accepting connections and let in-flight requests finish.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Error("http shutdown", "err", err)
	}

	// ctx is already cancelled, so workers claim no new jobs; wait (bounded) for
	// in-flight jobs to finish. Anything still running past the grace period is
	// left 'running' and recovered by the reaper on the next boot.
	select {
	case <-poolDone:
		log.Info("workers drained; bye")
	case <-time.After(shutdownGrace):
		log.Warn("workers did not drain in time; stuck jobs will be reaped on next boot")
	}
}

// newLogger builds the process-wide structured logger. LOG_LEVEL=debug raises
// verbosity; anything else defaults to info.
func newLogger() *slog.Logger {
	level := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
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
