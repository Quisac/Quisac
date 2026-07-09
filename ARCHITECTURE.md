# Quisac Architecture

Quisac builds a **code dependency graph** of a repository and keeps it live in
Neo4j. The graph is created once by a full index (from a repo tarball) and then
maintained incrementally by GitHub push webhooks — fetching only the files that
changed, never re-downloading the repo. The persisted graph is the substrate for
downstream incident-resolution features (graph traversal, blast-radius, etc.).

## Component diagram

```
                          ┌──────────────┐
        browser  ───────▶ │   server     │  HTTP: auth, /api/index, /api/graph,
                          │  (internal/  │        /webhook
        GitHub  ────────▶ │   server)    │
        webhooks          └──────┬───────┘
                                 │ calls (no graph logic here)
                                 ▼
                          ┌──────────────┐      narrow interfaces:
                          │   ingest     │      RepoSource, GraphStore
                          │ (orchestration)│
                          └──┬────────┬──┘
                  RepoSource │        │ GraphStore
                             ▼        ▼
                   ┌────────────┐   ┌──────────────┐
                   │  github    │   │  graphstore  │──▶ Neo4j
                   │ (App API)  │   │  (Cypher)    │
                   └────────────┘   └──────────────┘
                             ▲             ▲
             filewalk / resolver / treesitter / astparsers
             (parse a repo or a single file into a core.Graph)

  Auth/session state (users, sessions) lives in Postgres via internal/db.
```

## Package layout

```
cmd/quisac         entrypoint (--serve | --dir); wires deps, runs the HTTP
                   server + worker pool, handles graceful shutdown.
internal/
  server           HTTP transport only: routing, OAuth flow, webhook verify,
                   enqueue jobs, poll results, health/readiness, access logs.
                   No graph logic.
  worker           drains the durable job queue: claims jobs, runs them through
                   ingest, records outcomes; reaps stale jobs and evicts old ones.
  ingest           orchestration: FullIndex + ProcessPush. Depends only on the
                   RepoSource and GraphStore interfaces it declares.
  github           GitHub App client: DownloadRepo, GetFileContent, HeadSHA,
                   OAuth, installation repos.  (satisfies ingest.RepoSource)
  graphstore       Neo4j persistence: full + surgical writes, reads.
                   (satisfies ingest.GraphStore)
  core             the in-memory graph (Node/Edge/Graph, AddFile, PathFromID).
  filewalk         ReadRepo (full walk) and ParseFile (single file) → core.Graph.
  resolver         import string → repo-relative path, against a file manifest.
  treesitter       tree-sitter parsing → astparsers.Extracted.
  astparsers       language-specific symbol/edge extraction (py/js/ts/go).
  config           env config.        db  Postgres (users/sessions/jobs +
                                          embedded migrations).
  session          signed-cookie helper.
```

## Async work: durable queue + worker pool

Full indexes (user-initiated) and push updates (webhook-driven) are **durable
jobs** in the Postgres `jobs` table, not in-process goroutines. The server only
enqueues; a pool of workers claims and runs them. This survives restarts and
scales horizontally:

- **Crash-safe.** A job persists before its HTTP request is acked (webhooks get a
  non-2xx and retry if the enqueue fails). A worker that dies mid-job leaves the
  row `running`; the maintenance loop reaps it back to `pending` after a
  threshold that exceeds the job timeout, so live work is never reaped.
- **Serialized per repo, across replicas.** The partial unique index
  `jobs_one_running_per_repo` guarantees at most one `running` job per repo; the
  claim query (`FOR UPDATE SKIP LOCKED` + a not-running-for-this-repo filter)
  lets different repos run in parallel on any number of workers/replicas.
- **Retries + eviction.** Failures return to `pending` until `max_attempts`, then
  terminal `failed`; terminal jobs older than a TTL are deleted.
- **Rate limited.** A user may have at most `maxActiveIndexJobs` full-index jobs
  in flight, so one user can't queue unbounded expensive work.

Schema lives in `internal/db/migrations/*.sql`, applied on boot by `Store.Migrate`
(advisory-locked so concurrent booting replicas don't race; recorded in
`schema_migrations`). Liveness is `/healthz`; readiness (`/readyz`) probes
Postgres and Neo4j; process counters are at `/debug/vars`.

Dependencies point inward: `server → ingest → {core, filewalk, resolver}` and
the interface implementations (`github`, `graphstore`) are injected at
`cmd/quisac`. `ingest` never imports `github` or `graphstore` directly.

## Data model (Neo4j)

```
(:Repo {full_name, head_sha, indexed_at})

(:Symbol:<Type> {repo, id, name, type, path [, raw_imports]})
    Type ∈ File | Class | Function | Method | Module | Variable

(a:Symbol)-[:IMPORTS|CONTAINS|CALLS|INSTANTIATES|INHERITS|DECORATES]->(b:Symbol)
```

- Every symbol carries `repo`, so one instance is multi-tenant; `(repo, id)` is a
  uniqueness constraint (and the index behind traversal).
- `id` encodes ownership: `file:PATH`, `func:PATH::name`, `module:name`, etc.
  `path` is the repo-relative file a node belongs to (`""` for shared modules).
- `File.raw_imports` stores the file's raw import strings so the cross-file
  import layer can be recomputed on change without re-parsing other files.

The **import layer** (`File-[:IMPORTS]->File`) is *derived* from every file's
`raw_imports` resolved against the set of file paths (the manifest). Node and
intra-file edges are owned per file.

## Index paths

### Full index (`ingest.FullIndex`)
Used for the first index and as the drift/fallback path.
1. `github.DownloadRepo` → tarball → temp dir.
2. `filewalk.ReadRepo` walks source files (skipping deps/build/minified),
   parses each, resolves imports against the manifest → one `core.Graph`.
3. `graphstore.SaveGraph` replaces the repo's whole subgraph and records `head_sha`.

### Incremental update (`ingest.ProcessPush`)
Driven by push webhooks; serialized per repo.
1. Guard: if stored `head != push.before`, or the payload may be truncated
   (≥20 commits) → **full re-index**. Otherwise:
2. For each changed path, fetch **only that file** at the new SHA:
   - exists → `filewalk.ParseFile` → `graphstore.ReplaceFile` (delete that file's
     subgraph, insert the new one).
   - gone → `graphstore.DeleteFile`.
3. Recompute the import layer by blast radius:
   - **modify-only** push → re-resolve just the changed files' outbound imports
     (`SetImports`).
   - **structural** push (a file added/removed) → `RebuildImports` (inbound edges
     can shift when the manifest changes).
4. `SetHead` to the new SHA.

Cost of an incremental update is proportional to the change, not the repo size;
the repo tarball is never re-downloaded.

## Scaling

**Neo4j is not the bottleneck.** A 1M-LOC repo is on the order of 10–20k files
and a few hundred thousand nodes/edges — well within Neo4j's range. Traversal
queries (the product's real workload) are O(neighborhood), independent of total
graph size, because of the `(repo, id)` index. This is the reason the graph lives
in Neo4j rather than being rebuilt from JSON per request.

**Writes are surgical.** A push touching N files does O(N) fetches/parses and
per-file Cypher deletes/inserts; the graph is never fully loaded into the process
or rewritten wholesale on update. This keeps memory flat and update latency tied
to change size.

**Remaining levers (not yet implemented):**
- Structural pushes rebuild the whole `IMPORTS` edge layer (O(imports)). A token
  index (`import_keys` per file) would scope inbound recompute to true importers.
- Per-repo Neo4j databases (multi-database) for isolation at very high tenant counts.
- Parallel parsing and `CALL {} IN TRANSACTIONS` for very large initial indexes.

## Known limitations
- ≥20-commit pushes fall back to full re-index (GitHub truncates the payload's
  file lists); the robust fix is the Compare API.
- Removed files can leave orphan `Module` nodes (harmless; optional sweep).
- Cross-file edges are currently imports only; call edges are intra-file.
```
