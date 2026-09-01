package traverse

import (
	"context"
	"fmt"
	"sort"

	"quisac/internal/core"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// FaultRole says why a node is in the subgraph.
const (
	RoleEntry     = "entry"     // the symbol the failure was traced to
	RoleCandidate = "candidate" // a ranked root-cause candidate
	RoleLink      = "link"      // neither, but on a path between the two
)

// FaultNode carries the ranking context that made a node interesting, so the
// graph can be coloured without a second lookup.
type FaultNode struct {
	ID       string  `json:"id"`
	Type     string  `json:"type"`
	Name     string  `json:"name"`
	FilePath string  `json:"file_path,omitempty"`
	Role     string  `json:"role"`
	Rank     int     `json:"rank,omitempty"` // 1-based candidate rank; 0 when not a candidate
	Score    float64 `json:"score,omitempty"`
	Depth    int     `json:"depth"`
}

// FaultGraph is the subgraph behind one incident's traversal.
type FaultGraph struct {
	Repo        string      `json:"repo"`
	EntryPoints []string    `json:"entry_points"`
	Nodes       []FaultNode `json:"nodes"`
	Edges       []core.Edge `json:"edges"`

	// Truncated reports that the path scan hit its ceiling.
	Truncated bool `json:"truncated"`
}

// FaultQuery asks for the subgraph behind a stored traversal result.
type FaultQuery struct {
	Repo          string
	EntryPointIDs []string        // BFS seeds; the first is the primary entry point
	Candidates    []CandidateNode // ranked candidates, best first
	MaxDepth      int
}

// Ceilings for one subgraph query: a rendered graph stops being readable long
// before Neo4j stops being able to return one.
const (
	faultCandidates = 15  // top-ranked candidates used as path sources
	faultPathLimit  = 400 // paths expanded before the scan is cut short
)

// relEdgeTypes maps Neo4j relationship types back onto core.Edge's spelling.
var relEdgeTypes = map[string]string{
	"CALLS": "Calls", "IMPORTS": "Imports", "CONTAINS": "Contains",
}

// FaultGraph returns the nodes and relationships connecting q's candidates back
// to its entry points. Run discards the paths it walks, so they are recovered
// here from the stored incident row, long after the traversal finished.
//
// A stored result keeps only the primary entry point, so a candidate found via a
// secondary seed has no path to draw. Those still appear, without edges.
func (t *Traverser) FaultGraph(ctx context.Context, q FaultQuery) (*FaultGraph, error) {
	if q.Repo == "" || len(q.EntryPointIDs) == 0 {
		return nil, fmt.Errorf("traverse: fault graph needs a repo and an entry point")
	}
	maxDepth := q.MaxDepth
	if maxDepth < 1 || maxDepth > 10 {
		maxDepth = defaultMaxDepth
	}

	candidates := q.Candidates
	if len(candidates) > faultCandidates {
		candidates = candidates[:faultCandidates]
	}

	// Seeds must appear; everything else is discovered on a path.
	meta := make(map[string]FaultNode, len(candidates)+len(q.EntryPointIDs))
	for i, c := range candidates {
		if c.ID == "" {
			continue
		}
		meta[c.ID] = FaultNode{
			ID: c.ID, Type: c.Type, Name: c.Name, FilePath: c.FilePath,
			Role: RoleCandidate, Rank: i + 1, Score: c.Score, Depth: c.Depth,
		}
	}
	for _, id := range q.EntryPointIDs {
		if id == "" {
			continue
		}
		n := meta[id] // an entry point is usually also candidate #1
		n.ID, n.Role, n.Depth = id, RoleEntry, 0
		meta[id] = n
	}

	sourceIDs := make([]string, 0, len(meta))
	for id := range meta {
		sourceIDs = append(sourceIDs, id)
	}

	session := t.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	edges, seen, truncated, err := t.faultEdges(ctx, session, q.Repo, q.EntryPointIDs, sourceIDs, maxDepth)
	if err != nil {
		return nil, err
	}

	// Fill in properties for seeds no path touched, so every candidate is drawn.
	missing := make([]string, 0, len(meta))
	for id := range meta {
		if _, ok := seen[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		props, err := t.faultNodeProps(ctx, session, q.Repo, missing)
		if err != nil {
			return nil, err
		}
		for id, p := range props {
			seen[id] = p
		}
	}

	// Graph properties win on identity; the incident row wins on why it matters.
	nodes := make([]FaultNode, 0, len(seen))
	for id, n := range seen {
		if m, ok := meta[id]; ok {
			n.Role, n.Rank, n.Score, n.Depth = m.Role, m.Rank, m.Score, m.Depth
			if n.Name == "" {
				n.Name, n.Type, n.FilePath = m.Name, m.Type, m.FilePath
			}
		} else {
			n.Role = RoleLink
		}
		nodes = append(nodes, n)
	}
	sortFaultNodes(nodes)

	return &FaultGraph{
		Repo:        q.Repo,
		EntryPoints: q.EntryPointIDs,
		Nodes:       nodes,
		Edges:       edges,
		Truncated:   truncated,
	}, nil
}

// faultEdges expands the paths from each source back to an entry point.
//
// Both endpoints' properties are read off the relationship, keeping this to a
// single round trip.
func (t *Traverser) faultEdges(
	ctx context.Context,
	session neo4j.SessionWithContext,
	repo string,
	startIDs, sourceIDs []string,
	maxDepth int,
) ([]core.Edge, map[string]FaultNode, bool, error) {
	// Cypher will not take a parameter as a variable-length bound; maxDepth is
	// clamped to 1–10 above, so interpolating it is safe.
	cypher := fmt.Sprintf(`
		MATCH (start:Symbol {repo: $repo})
		WHERE start.id IN $startIDs
		MATCH p = (n:Symbol {repo: $repo})-[:CALLS|IMPORTS|CONTAINS*0..%d]->(start)
		WHERE n.id IN $sourceIDs
		WITH p ORDER BY length(p) ASC LIMIT $pathLimit
		WITH collect(p) AS paths, count(*) AS pathCount
		UNWIND paths AS p
		UNWIND relationships(p) AS r
		WITH DISTINCT r, pathCount
		RETURN startNode(r).id   AS fromID,
		       startNode(r).type AS fromType,
		       startNode(r).name AS fromName,
		       startNode(r).path AS fromPath,
		       endNode(r).id     AS toID,
		       endNode(r).type   AS toType,
		       endNode(r).name   AS toName,
		       endNode(r).path   AS toPath,
		       type(r)           AS rel,
		       pathCount         AS pathCount
	`, maxDepth)

	res, err := session.Run(ctx, cypher, map[string]any{
		"repo":      repo,
		"startIDs":  startIDs,
		"sourceIDs": sourceIDs,
		"pathLimit": faultPathLimit,
	})
	if err != nil {
		return nil, nil, false, fmt.Errorf("traverse: fault-graph path query: %w", err)
	}

	var (
		edges   []core.Edge
		seen    = map[string]FaultNode{}
		edgeSet = map[string]struct{}{}
		paths   int // paths the scan expanded, before relationships were unwound
	)
	record := func(id, typ, name, path string) {
		if id == "" {
			return
		}
		if _, ok := seen[id]; !ok {
			seen[id] = FaultNode{ID: id, Type: typ, Name: name, FilePath: path}
		}
	}
	for res.Next(ctx) {
		rec := res.Record()
		from, to := strVal(rec, "fromID"), strVal(rec, "toID")
		edgeType, ok := relEdgeTypes[strVal(rec, "rel")]
		if !ok || from == "" || to == "" {
			continue
		}
		record(from, strVal(rec, "fromType"), strVal(rec, "fromName"), strVal(rec, "fromPath"))
		record(to, strVal(rec, "toType"), strVal(rec, "toName"), strVal(rec, "toPath"))
		paths = intVal(rec, "pathCount")

		key := from + "\x00" + edgeType + "\x00" + to
		if _, dup := edgeSet[key]; dup {
			continue
		}
		edgeSet[key] = struct{}{}
		edges = append(edges, core.Edge{From: from, To: to, Type: edgeType})
	}
	if err := res.Err(); err != nil {
		return nil, nil, false, fmt.Errorf("traverse: fault-graph path cursor: %w", err)
	}
	// The scan stopped at the ceiling, so some connections are not drawn.
	return edges, seen, paths >= faultPathLimit, nil
}

// faultNodeProps loads identity properties for nodes by id.
func (t *Traverser) faultNodeProps(
	ctx context.Context,
	session neo4j.SessionWithContext,
	repo string,
	ids []string,
) (map[string]FaultNode, error) {
	res, err := session.Run(ctx, `
		MATCH (n:Symbol {repo: $repo})
		WHERE n.id IN $ids
		RETURN n.id AS id, n.type AS type, n.name AS name, n.path AS path
	`, map[string]any{"repo": repo, "ids": ids})
	if err != nil {
		return nil, fmt.Errorf("traverse: fault-graph node query: %w", err)
	}
	out := map[string]FaultNode{}
	for res.Next(ctx) {
		rec := res.Record()
		if id := strVal(rec, "id"); id != "" {
			out[id] = FaultNode{
				ID:       id,
				Type:     strVal(rec, "type"),
				Name:     strVal(rec, "name"),
				FilePath: strVal(rec, "path"),
			}
		}
	}
	if err := res.Err(); err != nil {
		return nil, fmt.Errorf("traverse: fault-graph node cursor: %w", err)
	}
	return out, nil
}

// sortFaultNodes orders the subgraph entry point, candidates, then link nodes.
// Neo4j's row order is the planner's, and a graph that reshuffles is unreadable.
func sortFaultNodes(nodes []FaultNode) {
	rolePriority := map[string]int{RoleEntry: 0, RoleCandidate: 1, RoleLink: 2}
	sort.SliceStable(nodes, func(i, j int) bool {
		a, b := nodes[i], nodes[j]
		if pa, pb := rolePriority[a.Role], rolePriority[b.Role]; pa != pb {
			return pa < pb
		}
		if a.Rank != b.Rank {
			return a.Rank < b.Rank
		}
		return a.ID < b.ID
	})
}
