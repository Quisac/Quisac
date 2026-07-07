package graphstore_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"quisac/internal/core"
	"quisac/internal/filewalk"
	"quisac/internal/graphstore"
)

// openStore connects to a live Neo4j, or skips when NEO4J_URI is unset so
// `go test ./...` stays hermetic. Run against a local instance with:
//
//	NEO4J_URI=neo4j://127.0.0.1:7687 NEO4J_USER=neo4j NEO4J_PASSWORD=... go test ./...
func openStore(t *testing.T) *graphstore.Store {
	t.Helper()
	uri, user, pass := os.Getenv("NEO4J_URI"), os.Getenv("NEO4J_USER"), os.Getenv("NEO4J_PASSWORD")
	if uri == "" || user == "" {
		t.Skip("NEO4J_URI/NEO4J_USER not set; skipping live Neo4j test")
	}
	store, err := graphstore.Open(context.Background(), uri, user, pass)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { store.Close(context.Background()) })
	return store
}

func TestSaveLoadRoundTrip(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.py"), "from utils import helper\n\ndef main():\n    helper()\n")
	writeFile(t, filepath.Join(dir, "utils.py"), "def helper():\n    pass\n")

	const repo = "quisac-test/roundtrip"
	g := filewalk.ReadRepo(dir)
	if err := store.SaveGraph(ctx, repo, "shaA", g); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := store.LoadGraph(ctx, repo)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Nodes) != len(g.Nodes) || len(got.Edges) != len(g.Edges) {
		t.Errorf("round-trip mismatch: got %d/%d, want %d/%d",
			len(got.Nodes), len(got.Edges), len(g.Nodes), len(g.Edges))
	}
	if head, _ := store.GetHead(ctx, repo); head != "shaA" {
		t.Errorf("head = %q, want shaA", head)
	}

	// Re-save must be idempotent, not additive.
	if err := store.SaveGraph(ctx, repo, "shaB", g); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	got2, _ := store.LoadGraph(ctx, repo)
	if len(got2.Nodes) != len(g.Nodes) || len(got2.Edges) != len(g.Edges) {
		t.Errorf("re-save changed counts: %d/%d", len(got2.Nodes), len(got2.Edges))
	}
}

// TestSurgicalWrites exercises the per-file primitives the incremental path uses.
func TestSurgicalWrites(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.py"), "from utils import helper\n\ndef main():\n    helper()\n")
	writeFile(t, filepath.Join(dir, "utils.py"), "def helper():\n    pass\n")

	const repo = "quisac-test/surgical"
	if err := store.SaveGraph(ctx, repo, "shaA", filewalk.ReadRepo(dir)); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Modify main.py: add a function via ReplaceFile.
	sub, err := filewalk.ParseFile("main.py", []byte("from utils import helper\n\ndef main():\n    helper()\n\ndef extra():\n    pass\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := store.ReplaceFile(ctx, repo, "main.py", sub); err != nil {
		t.Fatalf("replace: %v", err)
	}
	// The file→file import edge is separate; re-set it.
	if err := store.SetImports(ctx, repo, "main.py", []string{"utils.py"}); err != nil {
		t.Fatalf("set imports: %v", err)
	}

	g, _ := store.LoadGraph(ctx, repo)
	if _, ok := g.Nodes["func:main.py::extra"]; !ok {
		t.Error("ReplaceFile did not add extra()")
	}
	if !hasEdge(g, "file:main.py", "file:utils.py", "Imports") {
		t.Error("import edge missing after ReplaceFile+SetImports")
	}

	// Delete utils.py; its inbound import edge must vanish with it.
	if err := store.DeleteFile(ctx, repo, "utils.py"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	g, _ = store.LoadGraph(ctx, repo)
	if _, ok := g.Nodes["file:utils.py"]; ok {
		t.Error("DeleteFile left utils.py behind")
	}
	if hasEdge(g, "file:main.py", "file:utils.py", "Imports") {
		t.Error("stale import edge to deleted file")
	}
}

func hasEdge(g *core.Graph, from, to, typ string) bool {
	for _, e := range g.Edges {
		if e.From == from && e.To == to && e.Type == typ {
			return true
		}
	}
	return false
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
