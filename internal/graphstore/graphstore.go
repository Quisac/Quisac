// Package graphstore is the Neo4j persistence layer for the code graph.
//
// Data model:
//
//	(:Repo {full_name, head_sha, indexed_at})
//	(:Symbol:<Type> {repo, id, name, type, path[, raw_imports]})   Type ∈ File|Class|Function|Method|Variable
//	(a:Symbol)-[:IMPORTS|CONTAINS|CALLS|INSTANTIATES|INHERITS]->(b:Symbol)
//
// Every symbol carries a `repo` property so one instance holds many
// repositories; (repo, id) is unique. Writes are surgical: a full index
// replaces the whole repo subgraph, while an incremental update rewrites only
// the changed files (by their `path`) and the affected import edges.
//
// A full index is deliberately not one transaction: a repository large enough to
// matter produces more pending changes than one can hold. The head SHA is
// written last, so an index that dies half-way leaves no head and the next push
// re-indexes rather than trusting a partial graph.
package graphstore

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"quisac/internal/core"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// batchSize caps rows per UNWIND so large repos don't build one huge payload.
const batchSize = 1000

// Ceilings on uncommitted state: rows per transaction on the full-index path,
// and nodes detached per inner transaction when a repo is cleared.
const (
	writeBatchSize  = 5000
	deleteBatchSize = 10000
)

// maxIndexableIDBytes matches the ~8KB ceiling Neo4j's RANGE index enforces
// behind the (repo, id) constraint. An oversized ID means the parser captured a
// blob as a symbol, so it is skipped rather than failing the write.
const maxIndexableIDBytes = 8000

var nodeLabels = map[string]string{
	"File": "File", "Class": "Class", "Function": "Function",
	"Method": "Method", "Variable": "Variable",
}

var edgeRels = map[string]string{
	"Imports": "IMPORTS", "Contains": "CONTAINS", "Calls": "CALLS",
	"Instantiates": "INSTANTIATES", "Inherits": "INHERITS", "Decorates": "DECORATES",
}

var relEdges = func() map[string]string {
	m := make(map[string]string, len(edgeRels))
	for edge, rel := range edgeRels {
		m[rel] = edge
	}
	return m
}()

type Store struct {
	driver neo4j.DriverWithContext
}

// Open connects to uri, verifies connectivity, and ensures schema constraints.
func Open(ctx context.Context, uri, user, password string) (*Store, error) {
	driver, err := neo4j.NewDriverWithContext(uri, neo4j.BasicAuth(user, password, ""))
	if err != nil {
		return nil, fmt.Errorf("graphstore: new driver: %w", err)
	}
	if err := driver.VerifyConnectivity(ctx); err != nil {
		driver.Close(ctx)
		return nil, fmt.Errorf("graphstore: verify connectivity: %w", err)
	}
	s := &Store{driver: driver}
	if err := s.ensureSchema(ctx); err != nil {
		driver.Close(ctx)
		return nil, err
	}
	return s, nil
}

func (s *Store) Close(ctx context.Context) error {
	if s == nil || s.driver == nil {
		return nil
	}
	return s.driver.Close(ctx)
}

// Driver returns the underlying Neo4j driver, for traversal's own Cypher.
func (s *Store) Driver() neo4j.DriverWithContext {
	return s.driver
}

// Ping verifies connectivity to Neo4j. Used by the readiness probe.
func (s *Store) Ping(ctx context.Context) error {
	return s.driver.VerifyConnectivity(ctx)
}

func (s *Store) ensureSchema(ctx context.Context) error {
	return s.write(ctx, func(tx neo4j.ManagedTransaction) error {
		for _, stmt := range []string{
			`CREATE CONSTRAINT symbol_repo_id IF NOT EXISTS FOR (n:Symbol) REQUIRE (n.repo, n.id) IS UNIQUE`,
			`CREATE CONSTRAINT repo_full_name IF NOT EXISTS FOR (r:Repo) REQUIRE r.full_name IS UNIQUE`,
			// (repo, id) is the identity index, but incremental writes, the
			// manifest, and traversal all query by path — without these, each is
			// a label scan over every symbol in the instance.
			`CREATE INDEX symbol_repo_path IF NOT EXISTS FOR (n:Symbol) ON (n.repo, n.path)`,
			`CREATE INDEX file_repo_path IF NOT EXISTS FOR (n:File) ON (n.repo, n.path)`,
		} {
			if _, err := tx.Run(ctx, stmt, nil); err != nil {
				return err
			}
		}
		return nil
	})
}

// SaveGraph replaces the entire subgraph for repo with g and records headSHA.
func (s *Store) SaveGraph(ctx context.Context, repo, headSHA string, g *core.Graph) error {
	if err := s.clearRepo(ctx, repo); err != nil {
		return fmt.Errorf("graphstore: save graph %q: %w", repo, err)
	}
	if err := s.writeSubgraphBatched(ctx, repo, g); err != nil {
		return fmt.Errorf("graphstore: save graph %q: %w", repo, err)
	}
	// Last, on purpose: the head marks the graph complete, and ProcessPush
	// re-indexes a headless repo in full.
	return s.SetHead(ctx, repo, headSHA)
}

// clearRepo removes every symbol of a repo, committing as it goes. CALL … IN
// TRANSACTIONS cannot run inside an explicit transaction, which is the point:
// deleting a large repo's symbols in one exhausts the server's heap.
func (s *Store) clearRepo(ctx context.Context, repo string) error {
	session := s.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	res, err := session.Run(ctx, fmt.Sprintf(`
		MATCH (n:Symbol {repo:$repo})
		CALL { WITH n DETACH DELETE n } IN TRANSACTIONS OF %d ROWS`, deleteBatchSize),
		args{"repo": repo})
	if err != nil {
		return err
	}
	// An auto-commit result is lazy; consuming it is what runs the query.
	_, err = res.Consume(ctx)
	return err
}

// ReplaceFile removes path's subgraph and inserts the single-file subgraph g
// (its File node, owned children, and intra-file edges). File→file import edges
// are managed separately via SetImports. One file is small enough to stay a
// single transaction.
func (s *Store) ReplaceFile(ctx context.Context, repo, path string, g *core.Graph) error {
	err := s.write(ctx, func(tx neo4j.ManagedTransaction) error {
		if _, err := tx.Run(ctx, `MATCH (n:Symbol {repo:$repo, path:$path}) DETACH DELETE n`,
			args{"repo": repo, "path": path}); err != nil {
			return err
		}
		return writeSubgraph(ctx, tx, repo, g)
	})
	if err != nil {
		return fmt.Errorf("graphstore: replace file %q/%q: %w", repo, path, err)
	}
	return nil
}

// DeleteFile removes path's subgraph (File node, owned children, touching edges).
func (s *Store) DeleteFile(ctx context.Context, repo, path string) error {
	err := s.write(ctx, func(tx neo4j.ManagedTransaction) error {
		_, err := tx.Run(ctx, `MATCH (n:Symbol {repo:$repo, path:$path}) DETACH DELETE n`,
			args{"repo": repo, "path": path})
		return err
	})
	if err != nil {
		return fmt.Errorf("graphstore: delete file %q/%q: %w", repo, path, err)
	}
	return nil
}

// SetImports replaces the file→file import edges out of fromPath with edges to
// targets. File→module import edges are left untouched.
func (s *Store) SetImports(ctx context.Context, repo, fromPath string, targets []string) error {
	err := s.write(ctx, func(tx neo4j.ManagedTransaction) error {
		if _, err := tx.Run(ctx,
			`MATCH (:File {repo:$repo, path:$from})-[e:IMPORTS]->(:File {repo:$repo}) DELETE e`,
			args{"repo": repo, "from": fromPath}); err != nil {
			return err
		}
		if len(targets) == 0 {
			return nil
		}
		_, err := tx.Run(ctx,
			`MATCH (a:File {repo:$repo, path:$from})
			 UNWIND $targets AS t
			 MATCH (b:File {repo:$repo, path:t})
			 MERGE (a)-[:IMPORTS]->(b)`,
			args{"repo": repo, "from": fromPath, "targets": targets})
		return err
	})
	if err != nil {
		return fmt.Errorf("graphstore: set imports %q/%q: %w", repo, fromPath, err)
	}
	return nil
}

// RebuildImports replaces every file→file import edge for repo from the given
// (from, to) path pairs. Used after a structural change (files added/removed).
func (s *Store) RebuildImports(ctx context.Context, repo string, edges [][2]string) error {
	err := s.write(ctx, func(tx neo4j.ManagedTransaction) error {
		if _, err := tx.Run(ctx,
			`MATCH (:File {repo:$repo})-[e:IMPORTS]->(:File {repo:$repo}) DELETE e`,
			args{"repo": repo}); err != nil {
			return err
		}
		rows := make([]map[string]any, len(edges))
		for i, e := range edges {
			rows[i] = map[string]any{"from": e[0], "to": e[1]}
		}
		for _, chunk := range chunks(rows, batchSize) {
			if _, err := tx.Run(ctx,
				`UNWIND $rows AS row
				 MATCH (a:File {repo:$repo, path:row.from})
				 MATCH (b:File {repo:$repo, path:row.to})
				 MERGE (a)-[:IMPORTS]->(b)`,
				args{"repo": repo, "rows": chunk}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("graphstore: rebuild imports %q: %w", repo, err)
	}
	return nil
}

// FileManifest returns the set of file paths currently stored for repo.
func (s *Store) FileManifest(ctx context.Context, repo string) (map[string]bool, error) {
	out := map[string]bool{}
	err := s.read(ctx, func(res neo4j.ResultWithContext) error {
		for res.Next(ctx) {
			if path, ok := res.Record().Get("path"); ok {
				if p, ok := path.(string); ok {
					out[p] = true
				}
			}
		}
		return res.Err()
	}, `MATCH (f:File {repo:$repo}) RETURN f.path AS path`, args{"repo": repo})
	if err != nil {
		return nil, fmt.Errorf("graphstore: file manifest %q: %w", repo, err)
	}
	return out, nil
}

// RawImports returns the raw import strings of every file in repo, keyed by path.
func (s *Store) RawImports(ctx context.Context, repo string) (map[string][]string, error) {
	out := map[string][]string{}
	err := s.read(ctx, func(res neo4j.ResultWithContext) error {
		for res.Next(ctx) {
			rec := res.Record()
			path, _ := rec.Get("path")
			imports, _ := rec.Get("imports")
			if p, ok := path.(string); ok {
				out[p] = toStringSlice(imports)
			}
		}
		return res.Err()
	},
		`MATCH (f:File {repo:$repo}) WHERE f.raw_imports IS NOT NULL
		 RETURN f.path AS path, f.raw_imports AS imports`, args{"repo": repo})
	if err != nil {
		return nil, fmt.Errorf("graphstore: raw imports %q: %w", repo, err)
	}
	return out, nil
}

// LoadGraph reconstructs the full graph for repo (empty if never indexed).
func (s *Store) LoadGraph(ctx context.Context, repo string) (*core.Graph, error) {
	g := core.NewGraph()

	err := s.read(ctx, func(res neo4j.ResultWithContext) error {
		for res.Next(ctx) {
			rec := res.Record()
			id, _ := rec.Get("id")
			idStr, _ := id.(string)
			if idStr == "" {
				continue
			}
			typ, _ := rec.Get("type")
			name, _ := rec.Get("name")
			typStr, _ := typ.(string)
			nameStr, _ := name.(string)
			g.Nodes[idStr] = &core.Node{ID: idStr, Type: typStr, Name: nameStr}
			if raw, _ := rec.Get("raw_imports"); raw != nil {
				if imports := toStringSlice(raw); len(imports) > 0 {
					g.RawImports[core.PathFromID(idStr)] = imports
				}
			}
		}
		return res.Err()
	},
		`MATCH (n:Symbol {repo:$repo}) RETURN n.id AS id, n.type AS type, n.name AS name, n.raw_imports AS raw_imports`,
		args{"repo": repo})
	if err != nil {
		return nil, fmt.Errorf("graphstore: load nodes %q: %w", repo, err)
	}

	err = s.read(ctx, func(res neo4j.ResultWithContext) error {
		for res.Next(ctx) {
			rec := res.Record()
			from, _ := rec.Get("from")
			rel, _ := rec.Get("rel")
			to, _ := rec.Get("to")
			relStr, _ := rel.(string)
			edgeType, ok := relEdges[relStr]
			if !ok {
				continue
			}
			fromStr, _ := from.(string)
			toStr, _ := to.(string)
			g.Edges = append(g.Edges, core.Edge{From: fromStr, To: toStr, Type: edgeType})
		}
		return res.Err()
	},
		`MATCH (a:Symbol {repo:$repo})-[e]->(b:Symbol {repo:$repo})
		 RETURN a.id AS from, type(e) AS rel, b.id AS to`, args{"repo": repo})
	if err != nil {
		return nil, fmt.Errorf("graphstore: load edges %q: %w", repo, err)
	}
	return g, nil
}

// GetHead returns the last-indexed commit SHA for repo, or "" if never indexed.
func (s *Store) GetHead(ctx context.Context, repo string) (string, error) {
	var head string
	err := s.read(ctx, func(res neo4j.ResultWithContext) error {
		if res.Next(ctx) {
			if sha, ok := res.Record().Get("sha"); ok {
				head, _ = sha.(string)
			}
		}
		return res.Err()
	}, `MATCH (r:Repo {full_name:$repo}) RETURN r.head_sha AS sha`, args{"repo": repo})
	if err != nil {
		return "", fmt.Errorf("graphstore: get head %q: %w", repo, err)
	}
	return head, nil
}

// SetHead advances the recorded head commit without touching the subgraph.
func (s *Store) SetHead(ctx context.Context, repo, headSHA string) error {
	err := s.write(ctx, func(tx neo4j.ManagedTransaction) error {
		_, err := tx.Run(ctx,
			`MERGE (r:Repo {full_name:$repo}) SET r.head_sha=$sha, r.indexed_at=datetime()`,
			args{"repo": repo, "sha": headSHA})
		return err
	})
	if err != nil {
		return fmt.Errorf("graphstore: set head %q: %w", repo, err)
	}
	return nil
}

// subgraphRows flattens g into the batches the write queries UNWIND: nodes by
// symbol type, edges by relationship type. It is pure, so the same flattening
// serves the per-file write and the batched full index.
func subgraphRows(g *core.Graph) (nodes, edges map[string][]map[string]any) {
	nodes = map[string][]map[string]any{}
	for _, n := range g.Nodes {
		if _, ok := nodeLabels[n.Type]; !ok {
			continue
		}
		if len(n.ID) > maxIndexableIDBytes {
			slog.Warn("skipping oversized node", "type", n.Type, "bytes", len(n.ID),
				"id", preview(n.ID, 100))
			continue
		}
		path := core.PathFromID(n.ID)
		row := map[string]any{"id": n.ID, "name": n.Name, "type": n.Type, "path": path}
		if n.Type == "File" {
			row["raw_imports"] = g.RawImports[path]
		}
		nodes[n.Type] = append(nodes[n.Type], row)
	}

	edges = map[string][]map[string]any{}
	for _, e := range g.Edges {
		if rel, ok := edgeRels[e.Type]; ok {
			edges[rel] = append(edges[rel], map[string]any{"from": e.From, "to": e.To})
		}
	}
	return nodes, edges
}

// createNodes is the insert for one symbol type. The caller has already deleted
// what it replaces, so this CREATEs rather than MERGEs.
func createNodes(typ string) string {
	return fmt.Sprintf(
		`UNWIND $rows AS row
		 CREATE (n:Symbol:%s {repo:$repo, id:row.id, name:row.name, type:row.type, path:row.path, raw_imports:row.raw_imports})`,
		nodeLabels[typ])
}

// createEdges is the insert for one relationship type.
func createEdges(rel string) string {
	return fmt.Sprintf(
		`UNWIND $rows AS row
		 MATCH (a:Symbol {repo:$repo, id:row.from})
		 MATCH (b:Symbol {repo:$repo, id:row.to})
		 CREATE (a)-[:%s]->(b)`, rel)
}

// writeSubgraph inserts g inside the caller's transaction. Used for per-file
// writes, which are bounded by one file's symbols.
func writeSubgraph(ctx context.Context, tx neo4j.ManagedTransaction, repo string, g *core.Graph) error {
	nodes, edges := subgraphRows(g)
	for typ, rows := range nodes {
		for _, chunk := range chunks(rows, batchSize) {
			if _, err := tx.Run(ctx, createNodes(typ), args{"repo": repo, "rows": chunk}); err != nil {
				return err
			}
		}
	}
	for rel, rows := range edges {
		for _, chunk := range chunks(rows, batchSize) {
			if _, err := tx.Run(ctx, createEdges(rel), args{"repo": repo, "rows": chunk}); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeSubgraphBatched inserts g one transaction per chunk, so a repository's
// size bounds how long the write takes rather than whether it fits. Every node
// is committed before any relationship: an edge MATCHes both endpoints, so the
// nodes must already be visible to other transactions.
func (s *Store) writeSubgraphBatched(ctx context.Context, repo string, g *core.Graph) error {
	nodes, edges := subgraphRows(g)

	// One session for every batch: leasing a connection per batch would be most
	// of the cost across thousands of transactions.
	session := s.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	run := func(cypher string, chunk []map[string]any) error {
		_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
			_, err := tx.Run(ctx, cypher, args{"repo": repo, "rows": chunk})
			return nil, err
		})
		return err
	}

	for typ, rows := range nodes {
		q := createNodes(typ)
		for _, chunk := range chunks(rows, writeBatchSize) {
			if err := run(q, chunk); err != nil {
				return err
			}
		}
	}
	for rel, rows := range edges {
		q := createEdges(rel)
		for _, chunk := range chunks(rows, writeBatchSize) {
			if err := run(q, chunk); err != nil {
				return err
			}
		}
	}
	return nil
}

// args is a short alias for Cypher parameter maps.
type args = map[string]any

func (s *Store) write(ctx context.Context, fn func(tx neo4j.ManagedTransaction) error) error {
	session := s.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)
	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		return nil, fn(tx)
	})
	return err
}

func (s *Store) read(ctx context.Context, consume func(res neo4j.ResultWithContext) error, cypher string, params args) error {
	session := s.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)
	res, err := session.Run(ctx, cypher, params)
	if err != nil {
		return err
	}
	return consume(res)
}

func chunks(rows []map[string]any, size int) [][]map[string]any {
	var out [][]map[string]any
	for i := 0; i < len(rows); i += size {
		out = append(out, rows[i:min(i+size, len(rows))])
	}
	return out
}

func toStringSlice(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func preview(s string, n int) string {
	if len(s) > n {
		s = s[:n]
	}
	return strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s)
}
