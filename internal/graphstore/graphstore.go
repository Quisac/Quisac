// Package graphstore is the Neo4j persistence layer for the code graph.
//
// Data model:
//
//	(:Repo {full_name, head_sha, indexed_at})
//	(:Symbol:<Type> {repo, id, name, type, path[, raw_imports]})   Type ∈ File|Class|Function|Method|Module|Variable
//	(a:Symbol)-[:IMPORTS|CONTAINS|CALLS|INSTANTIATES|INHERITS|DECORATES]->(b:Symbol)
//
// Every symbol carries a `repo` property so one instance holds many
// repositories; (repo, id) is unique. Writes are surgical: a full index
// replaces the whole repo subgraph, while an incremental update rewrites only
// the changed files (by their `path`) and the affected import edges.
package graphstore

import (
	"context"
	"fmt"
	"strings"

	"quisac/internal/core"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// batchSize caps rows per UNWIND so large repos don't build one huge payload.
const batchSize = 1000

// maxIndexableIDBytes bounds a node ID's size — Neo4j's RANGE index (behind the
// (repo, id) constraint) rejects values larger than ~8KB. An oversized ID means
// the parser captured a blob as a symbol; we skip it rather than fail the write.
const maxIndexableIDBytes = 8000

var nodeLabels = map[string]string{
	"File": "File", "Class": "Class", "Function": "Function",
	"Method": "Method", "Module": "Module", "Variable": "Variable",
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

// Ping verifies connectivity to Neo4j. Used by the readiness probe.
func (s *Store) Ping(ctx context.Context) error {
	return s.driver.VerifyConnectivity(ctx)
}

func (s *Store) ensureSchema(ctx context.Context) error {
	return s.write(ctx, func(tx neo4j.ManagedTransaction) error {
		for _, stmt := range []string{
			`CREATE CONSTRAINT symbol_repo_id IF NOT EXISTS FOR (n:Symbol) REQUIRE (n.repo, n.id) IS UNIQUE`,
			`CREATE CONSTRAINT repo_full_name IF NOT EXISTS FOR (r:Repo) REQUIRE r.full_name IS UNIQUE`,
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
	err := s.write(ctx, func(tx neo4j.ManagedTransaction) error {
		if _, err := tx.Run(ctx, `MATCH (n:Symbol {repo:$repo}) DETACH DELETE n`, args{"repo": repo}); err != nil {
			return err
		}
		if _, err := tx.Run(ctx,
			`MERGE (r:Repo {full_name:$repo}) SET r.head_sha=$sha, r.indexed_at=datetime()`,
			args{"repo": repo, "sha": headSHA}); err != nil {
			return err
		}
		return writeSubgraph(ctx, tx, repo, g)
	})
	if err != nil {
		return fmt.Errorf("graphstore: save graph %q: %w", repo, err)
	}
	return nil
}

// ReplaceFile removes path's subgraph and inserts the single-file subgraph g
// (its File node, owned children, referenced Module nodes, and intra-file
// edges). File→file import edges are managed separately via SetImports.
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
		for _, chunk := range chunks(rows) {
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

// writeSubgraph inserts g's nodes and edges. Module nodes are MERGEd (shared
// across files); owned nodes are CREATEd (the caller has deleted the old ones).
func writeSubgraph(ctx context.Context, tx neo4j.ManagedTransaction, repo string, g *core.Graph) error {
	var modules []map[string]any
	owned := map[string][]map[string]any{}
	for _, n := range g.Nodes {
		if _, ok := nodeLabels[n.Type]; !ok {
			continue
		}
		if len(n.ID) > maxIndexableIDBytes {
			fmt.Printf("graphstore: skipping oversized %s node (%d bytes): %q…\n",
				n.Type, len(n.ID), preview(n.ID, 100))
			continue
		}
		if n.Type == "Module" {
			modules = append(modules, map[string]any{"id": n.ID, "name": n.Name})
			continue
		}
		path := core.PathFromID(n.ID)
		row := map[string]any{"id": n.ID, "name": n.Name, "type": n.Type, "path": path}
		if n.Type == "File" {
			row["raw_imports"] = g.RawImports[path]
		}
		owned[n.Type] = append(owned[n.Type], row)
	}

	for _, chunk := range chunks(modules) {
		if _, err := tx.Run(ctx,
			`UNWIND $rows AS row
			 MERGE (n:Symbol:Module {repo:$repo, id:row.id})
			 ON CREATE SET n.name=row.name, n.type='Module', n.path=''`,
			args{"repo": repo, "rows": chunk}); err != nil {
			return err
		}
	}

	for typ, rows := range owned {
		q := fmt.Sprintf(
			`UNWIND $rows AS row
			 CREATE (n:Symbol:%s {repo:$repo, id:row.id, name:row.name, type:row.type, path:row.path, raw_imports:row.raw_imports})`,
			nodeLabels[typ])
		for _, chunk := range chunks(rows) {
			if _, err := tx.Run(ctx, q, args{"repo": repo, "rows": chunk}); err != nil {
				return err
			}
		}
	}

	byRel := map[string][]map[string]any{}
	for _, e := range g.Edges {
		if rel, ok := edgeRels[e.Type]; ok {
			byRel[rel] = append(byRel[rel], map[string]any{"from": e.From, "to": e.To})
		}
	}
	for rel, rows := range byRel {
		q := fmt.Sprintf(
			`UNWIND $rows AS row
			 MATCH (a:Symbol {repo:$repo, id:row.from})
			 MATCH (b:Symbol {repo:$repo, id:row.to})
			 CREATE (a)-[:%s]->(b)`, rel)
		for _, chunk := range chunks(rows) {
			if _, err := tx.Run(ctx, q, args{"repo": repo, "rows": chunk}); err != nil {
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

func chunks(rows []map[string]any) [][]map[string]any {
	var out [][]map[string]any
	for i := 0; i < len(rows); i += batchSize {
		out = append(out, rows[i:min(i+batchSize, len(rows))])
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
