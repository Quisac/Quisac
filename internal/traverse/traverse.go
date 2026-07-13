// Package traverse implements the reactive incident traversal engine.
//
// Given a PagerDuty incident (service name + error body), it:
//  1. Locates the best-matching entry-point node in the Neo4j graph.
//  2. Runs a backward BFS through CALLS/IMPORTS edges to find every caller up to
//     maxDepth hops away — these are the root-cause candidates.
//  3. Scores candidates by hop distance and whether their name appears in the
//     error body, so the most suspicious symbol surfaces first.
//
// Neo4j natively executes variable-length path patterns as BFS, so we delegate
// the graph walk to Cypher and only apply scoring in Go.
package traverse

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"quisac/internal/core"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// ─── Public types ────────────────────────────────────────────────────────────

// Incident is the normalised payload extracted from a PagerDuty webhook. It is
// the input to Run.
type Incident struct {
	ID             string // PagerDuty incident ID
	InstallationID string
	Title          string // human-readable title
	ServiceName    string // PD service summary → used to locate the repo entry point
	ErrorBody      string // alert body / stack trace / custom_details
}

// CandidateNode is a symbol identified as a likely root-cause contributor.
type CandidateNode struct {
	ID       string  `json:"id"`
	Type     string  `json:"type"`     // Function | Method | File | …
	Name     string  `json:"name"`
	FilePath string  `json:"file_path"`
	Depth    int     `json:"depth"`    // hops back from the entry point (1 = direct caller)
	Score    float64 `json:"score"`    // higher = more suspicious
}

// Result is the full output of a traversal run.
type Result struct {
	Repo       string          `json:"repo"`
	EntryPoint *core.Node      `json:"entry_point"` // where the BFS started
	Candidates []CandidateNode `json:"candidates"`  // ordered by Score desc
}

// ─── Traverser ───────────────────────────────────────────────────────────────

// Traverser performs incident-driven BFS over the Neo4j graph.
type Traverser struct {
	driver neo4j.DriverWithContext
}

// New returns a Traverser backed by the supplied Neo4j driver.
func New(driver neo4j.DriverWithContext) *Traverser {
	return &Traverser{driver: driver}
}

// Run is the top-level entry point.
//
//   - repo      – full_name of the indexed repository (e.g. "acme/billing-api")
//   - incident  – normalized PagerDuty payload
//   - maxDepth  – maximum hops to traverse backward (recommended: 5)
func (t *Traverser) Run(ctx context.Context, repo string, incident Incident, maxDepth int) (*Result, error) {
	if maxDepth < 1 || maxDepth > 10 {
		maxDepth = 5
	}

	entry, err := t.findEntryPoint(ctx, repo, incident)
	if err != nil {
		return nil, fmt.Errorf("traverse: find entry point: %w", err)
	}

	candidates, err := t.bfsBacktrace(ctx, repo, entry.ID, maxDepth, incident.ErrorBody)
	if err != nil {
		return nil, fmt.Errorf("traverse: BFS backtrace: %w", err)
	}

	return &Result{
		Repo:       repo,
		EntryPoint: entry,
		Candidates: candidates,
	}, nil
}

// ─── Entry-point discovery ───────────────────────────────────────────────────

// findEntryPoint finds the best-matching Symbol node for the incident.
//
// Strategy (in priority order):
//  1. Tokens from the error body that exactly match a Function/Method name.
//  2. Tokens from the service name that partially match a Function/Method name.
//  3. Fall back to any File whose path contains a service-name token.
func (t *Traverser) findEntryPoint(ctx context.Context, repo string, incident Incident) (*core.Node, error) {
	terms := extractTerms(incident.ErrorBody, incident.ServiceName)
	if len(terms) == 0 {
		return nil, fmt.Errorf("no usable terms found in incident (service=%q)", incident.ServiceName)
	}

	session := t.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	// Cypher: for each term, find the highest-priority matching Symbol.
	// Type priority: Function/Method > Class > File > other.
	res, err := session.Run(ctx, `
		UNWIND $terms AS term
		MATCH (n:Symbol {repo: $repo})
		WHERE n.type IN ['Function', 'Method', 'Class', 'File']
		  AND (toLower(n.name) CONTAINS toLower(term)
		    OR toLower(n.path) CONTAINS toLower(term))
		WITH n, term
		WITH n,
		     count(term) AS termMatches,
		     max(CASE WHEN toLower(n.name) = toLower(term) THEN 10 ELSE 0 END) AS exactMatchBonus
		WITH n, termMatches, exactMatchBonus,
		     CASE n.type
		       WHEN 'Function' THEN 4
		       WHEN 'Method'   THEN 4
		       WHEN 'Class'    THEN 2
		       WHEN 'File'     THEN 1
		       ELSE 0
		     END AS typeScore
		RETURN n.id AS id, n.type AS type, n.name AS name, n.path AS path,
		       (termMatches * 2) + exactMatchBonus + typeScore AS score
		ORDER BY score DESC
		LIMIT 1
	`, map[string]any{
		"repo":  repo,
		"terms": terms,
	})
	if err != nil {
		return nil, fmt.Errorf("entry-point query: %w", err)
	}
	if res.Next(ctx) {
		rec := res.Record()
		return &core.Node{
			ID:   strVal(rec, "id"),
			Type: strVal(rec, "type"),
			Name: strVal(rec, "name"),
		}, nil
	}
	if err := res.Err(); err != nil {
		return nil, fmt.Errorf("entry-point cursor: %w", err)
	}
	return nil, fmt.Errorf("no matching node for service %q in repo %q (terms=%v)",
		incident.ServiceName, repo, terms)
}

// ─── BFS back-tracing ────────────────────────────────────────────────────────

// bfsBacktrace traverses backward through CALLS and IMPORTS edges from
// startNodeID, up to maxDepth hops. Neo4j evaluates variable-length path
// patterns using BFS internally, so the traversal is efficient.
//
// Nodes are scored by:
//   - inverse of depth (closer callers are more suspicious)
//   - +0.5 if the node's name appears in the error body
//   - +0.2 if the node is a Function or Method (vs a File)
func (t *Traverser) bfsBacktrace(ctx context.Context, repo, startID string, maxDepth int, errorBody string) ([]CandidateNode, error) {
	session := t.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	// Neo4j v5 supports integer literals in variable-length range; we embed
	// maxDepth as a literal (validated to 1–10 above) so the query planner can
	// use it as a bound. Parameterising the upper bound is not supported in all
	// Cypher planners, so we use Sprintf here — the value is always an integer.
	cypher := fmt.Sprintf(`
		MATCH (start:Symbol {repo: $repo, id: $startID})
		MATCH path = (caller:Symbol {repo: $repo})-[:CALLS|IMPORTS*1..%d]->(start)
		WITH caller, min(length(path)) AS depth
		WHERE caller.id <> $startID
		RETURN caller.id   AS id,
		       caller.type AS type,
		       caller.name AS name,
		       caller.path AS path,
		       depth
		ORDER BY depth ASC
		LIMIT 50
	`, maxDepth)

	res, err := session.Run(ctx, cypher, map[string]any{
		"repo":    repo,
		"startID": startID,
	})
	if err != nil {
		return nil, fmt.Errorf("BFS query: %w", err)
	}

	// Build a set of lower-cased tokens from the error body for scoring.
	errorTokens := tokenSet(extractTerms(errorBody, ""))

	var candidates []CandidateNode
	for res.Next(ctx) {
		rec := res.Record()
		nameStr  := strVal(rec, "name")
		typStr   := strVal(rec, "type")
		depthInt := intVal(rec, "depth")

		// Base score: closer = higher.
		score := 1.0 / float64(depthInt+1)
		// Boost if this symbol appears in the error body.
		if errorTokens[strings.ToLower(nameStr)] {
			score += 0.5
		}
		// Prefer actionable code symbols over file nodes.
		if typStr == "Function" || typStr == "Method" {
			score += 0.2
		}

		candidates = append(candidates, CandidateNode{
			ID:       strVal(rec, "id"),
			Type:     typStr,
			Name:     nameStr,
			FilePath: strVal(rec, "path"),
			Depth:    depthInt,
			Score:    score,
		})
	}
	if err := res.Err(); err != nil {
		return nil, fmt.Errorf("BFS cursor: %w", err)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})
	return candidates, nil
}

// ─── Text extraction helpers ─────────────────────────────────────────────────

// extractTerms returns de-duplicated, non-trivial tokens from the error body
// and service name. These are used both to find the entry-point node and to
// score BFS candidates.
func extractTerms(errorBody, serviceName string) []string {
	seen := map[string]bool{}
	var terms []string

	add := func(s string) {
		s = strings.TrimSpace(s)
		// Skip short or common words that would match too broadly.
		if len(s) < 3 || stopWord[strings.ToLower(s)] {
			return
		}
		if !seen[s] {
			seen[s] = true
			terms = append(terms, s)
		}
	}

	for _, tok := range tokenize(serviceName) {
		add(tok)
	}
	for _, line := range strings.Split(errorBody, "\n") {
		for _, tok := range tokenize(line) {
			add(tok)
		}
	}
	return terms
}

// tokenize splits s on common delimiters used in paths, identifiers, and stack
// trace lines.
func tokenize(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case '-', '.', '/', ':', ' ', '\t', '(', ')', '[', ']', ',', '"', '\'', '`', '#', '@':
			return true
		}
		return false
	})
}

func tokenSet(terms []string) map[string]bool {
	m := make(map[string]bool, len(terms))
	for _, t := range terms {
		m[strings.ToLower(t)] = true
	}
	return m
}

// stopWord contains tokens that are too common to be useful search terms.
var stopWord = map[string]bool{
	"the": true, "and": true, "for": true, "not": true, "are": true,
	"was": true, "has": true, "at": true, "in": true, "on": true,
	"to": true, "of": true, "a": true, "an": true, "is": true,
	"err": true, "error": true, "line": true, "func": true, "type": true,
	"null": true, "nil": true, "true": true, "false": true, "new": true,
	"got": true, "get": true, "set": true, "run": true, "main": true,
}

// ─── Neo4j result helpers ─────────────────────────────────────────────────────

func strVal(rec *neo4j.Record, key string) string {
	v, _ := rec.Get(key)
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

func intVal(rec *neo4j.Record, key string) int {
	v, _ := rec.Get(key)
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}
