# Quisac Architecture

Quisac turns a production alert into a pull request.

It builds a **code dependency graph** of a repository and keeps it live in Neo4j.
The graph is created once by a full index (from a repo tarball) and then
maintained incrementally by GitHub push webhooks — fetching only the files that
changed, never re-downloading the repo.

That graph is the product's central bet: **structure beats context stuffing**.
When an alert fires, the graph narrows a whole repository to a handful of ranked
symbols before any model is involved — so a fix is written from two or three
files instead of a repo dumped into a context window. Known errors never reach a
model at all.

## Component diagram

```
                          ┌──────────────┐
        browser  ───────▶ │   server     │  HTTP: auth, /api/index, /api/graph,
                          │  (internal/  │        /api/incidents[/{id}/graph]
     GitHub / PagerDuty ─▶│   server)    │  Webhooks: /webhook, /pagerduty/webhook
                          └──────┬───────┘
                                 │ enqueues durable jobs only — no work inline
                                 ▼
                          ┌──────────────┐
                          │    worker    │  full_index | push | resolve
                          └──┬────────┬──┘
                             │        │
                             ▼        ▼
             ┌──────────────┐        ┌──────────────────────────────┐
             │   ingest     │        │          resolve             │
             │ (index graph)│        │  (incident → pull request)   │
             └──┬────────┬──┘        └──┬────────┬─────────┬────────┘
     RepoSource │        │ GraphStore   │        │         │
                │        │        tier 1│  tier 2│   tier 3│
                │        │              ▼        ▼         ▼
                │        │      ┌──────────┐ ┌────────┐ ┌─────┐
                │        │      │ patterns │ │traverse│ │ llm │
                │        │      │ (cache)  │ │ (BFS)  │ │(fix)│
                │        │      └──────────┘ └───┬────┘ └──┬──┘
                │        │                       │         │
                ▼        ▼                       ▼         ▼
      ┌────────────┐   ┌──────────────┐      Neo4j    ┌────────┐
      │  github    │   │  graphstore  │──▶ (write)    │ patch  │
      │ (App API)  │   │  (Cypher)    │               │(apply +│
      │ + open PR  │◀──────────────────────────────── │validate)│
      └────────────┘   └──────────────┘               └────────┘
                ▲             ▲
filewalk / resolver / treesitter / astparsers
(parse a repo or a single file into a core.Graph)

Auth/session state, jobs, incidents, fix patterns, and per-file change history
live in Postgres via internal/db.
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
  filewalk         ReadRepo (parallel full walk) and ParseFile (single file)
                   → core.Graph.
  resolver         import string → repo-relative path, against a file manifest.
  treesitter       tree-sitter parsing → astparsers.Extracted; Session reuses
                   one parser per language (not safe to share across goroutines).
  astparsers       language-specific symbol/edge extraction (py/js/ts/go).
  config           env config.        db  Postgres (users/sessions/jobs/incidents +
                                          embedded migrations).
  session          signed-cookie helper.
  pagerduty        webhook payload parsing and normalization.
  traverse         Neo4j graph traversal (BFS) + multi-signal candidate ranking,
                   and the fault subgraph behind a stored result.
  patterns         error fingerprinting and fuzzy matching (the fix cache).
  patch            exact-string edits: the fix representation, applied + validated.
  llm              Claude client that proposes a patch for a novel incident.
  resolve          the incident→pull-request pipeline that drives all of the above.
```

## Async work: durable queue + worker pool

Three kinds of work — full indexes (user-initiated), push updates
(webhook-driven), and incident resolutions (alert-driven) — are **durable jobs**
in the Postgres `jobs` table, not in-process goroutines. The server only
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
  Resolve jobs opt out of that lane by filing under `incident:<id>`, so an alert
  never queues behind an index of the repo it is about to traverse.
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
`cmd/quisac`. `ingest` never imports `github` or `graphstore` directly. The same
rule holds for the fix pipeline: `resolve` declares narrow `Store`, `Traverser`,
`Source`, and `Fixer` interfaces and is handed implementations at wiring time,
which is what lets the whole three-tier pipeline be tested against fakes.

## Postgres tables

| table | purpose |
|---|---|
| `users`, `sessions` | auth state |
| `jobs` | the durable queue (`full_index` / `push` / `resolve`) |
| `incidents` | alerts, their traversal result, and their resolution |
| `incident_patterns` | tier-1 fix cache, keyed by error fingerprint |
| `file_stats` | per-file churn, recency, and fault history |

## Data model (Neo4j)

```
(:Repo {full_name, head_sha, indexed_at})

(:Symbol:<Type> {repo, id, name, type, path [, raw_imports]})
    Type ∈ File | Class | Function | Method | Variable

(a:Symbol)-[:IMPORTS|CONTAINS|CALLS|INSTANTIATES|INHERITS]->(b:Symbol)
```

- Every symbol carries `repo`, so one instance is multi-tenant; `(repo, id)` is a
  uniqueness constraint, and `(repo, path)` is indexed on both `:Symbol` and
  `:File` — path, not id, is what incremental writes and traversal look up.
- `id` encodes ownership: `file:PATH`, `func:PATH::name`, `class:PATH::Name`.
  `path` is the repo-relative file a node belongs to.
- **Only symbols that exist in the repo are nodes.** A name the parser cannot
  place inside the repo — an npm package, the standard library, a decorator, a
  base class from a dependency — is recorded on the importing file rather than
  given a node. Such nodes were 54% of the graph and 72% of its edges, and none
  could ever appear in a traversal result: with no outgoing
  `CALLS`/`IMPORTS`/`CONTAINS` edge, no path runs through one back to a failure.
- `File.raw_imports` stores the file's raw import strings so the cross-file
  import layer can be recomputed on change without re-parsing other files, and
  so a viewer can still see what a file depends on externally.

The **import layer** (`File-[:IMPORTS]->File`) is *derived* from every file's
`raw_imports` resolved against the set of file paths (the manifest). Node and
intra-file edges are owned per file.

## Index paths

### Full index (`ingest.FullIndex`)
Used for the first index and as the drift/fallback path.
1. `github.DownloadRepo` → tarball → temp dir.
2. `filewalk.ReadRepo` scans for source files (skipping deps/build/minified),
   parses them across a worker pool — one tree-sitter session per worker — then
   assembles the graph on one goroutine in scan order and resolves imports
   against the manifest → one `core.Graph`.
3. `graphstore.SaveGraph` replaces the repo's whole subgraph, then records
   `head_sha` **last**. The clear runs as `CALL {} IN TRANSACTIONS` and the
   inserts are one transaction per batch, so a repository's size decides how long
   the write takes rather than whether it fits in memory. The cost is atomicity:
   an index that dies half-way leaves the repo headless, which `ProcessPush`
   already treats as "index this fully".

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

## Incident response: alert → pull request

An alert enters through `/api/pagerduty/webhook?installation_id=...`, where the
`installation_id` scopes it to one tenant's indexed repositories. The handler
does exactly two things — persist the incident and enqueue a `resolve` job —
then returns. **Resolution is a durable job, not a goroutine**: a pipeline that
can open pull requests must survive a deploy restarting mid-incident, and it
retries at most twice (rather than the queue's default five) because each attempt
costs model tokens and can push a branch.

Resolve jobs are filed under the synthetic repo key `incident:<id>` rather than a
repository. The queue serialises at most one running job per repo, which is right
for indexing and wrong for incident response — an alert must not queue behind a
twenty-minute full index of the repo it is about to traverse.

`internal/resolve` then runs three tiers, cheapest first, stopping at the first
one that produces a fix. **Every tier may fall through**: a cached fix whose
anchors no longer match today's code does not dead-end the incident, it falls
through to the graph, and the graph falls through to the model.

### Tier 1 — pattern cache (`internal/patterns`)

Most production incidents are not novel: the same pool exhausts, the same field
is nil, with a new incident ID and new timestamps every time. So a fix is stored
against the *shape* of the error, not its text.

`patterns.Analyze` builds a **signature** — the error class and message with every
volatile substring masked (`<uuid>`, `<ts>`, `<addr>`, `<hex>`, `<str>`, `<n>`),
plus the top stack frame (path and function, deliberately **not** the line number,
so the bug keeps its identity when the file above it grows), plus the service.
Its sha256 is the fingerprint.

An exact fingerprint hit resolves the incident with **no graph walk and no model
call**. Failing that, a bounded scan of the installation's recent patterns is
compared by Jaccard overlap of token bags, accepted only above 0.75 — applying
the wrong cached fix is far worse than falling through.

### Tier 2 — graph traversal (`internal/traverse`)

1. **Stack frames.** `ExtractFrames` parses Python, Node/V8, Java/Kotlin, Ruby,
   and Go traces, normalising every language to innermost-frame-first. A frame is
   direct evidence that a file was executing; a term match is only evidence that
   a word appeared in the payload.
2. **Frame resolution.** `RankFrames` maps a runtime-reported path
   (`/srv/app/billing/refund.py`) onto the indexed path (`billing/refund.py`) by
   the longest path suffix that lands on exactly **one** file — tolerating any
   build prefix without letting a bare `index.js` match a dozen files.
3. **Entry points.** Up to three seeds, from the topmost resolved frames
   (preferring the function the runtime named), falling back to term matching for
   alerts that carry no trace at all.
4. **Backward walk.** One Cypher query walks `CALLS|IMPORTS|CONTAINS*0..N`
   backward from every seed, taking `min(length(path))` as depth and counting
   fan-in in the same pass. Depth 0 is included: the bug is often in the failing
   function itself. **`CONTAINS` is what makes the walk cross file boundaries** —
   call edges are intra-file, so reaching a function's file and then that file's
   importers is the path from a stack frame to the rest of the codebase.
5. **Ranking.** Eight signals, each normalised to 0–1, combined by weights that
   sum to 1.0 — so a score reads directly as a confidence:

   | signal | weight | source |
   |---|---|---|
   | proximity  | 0.28 | `0.7^depth`, geometric falloff per hop |
   | error term | 0.18 | the symbol is named in the alert body |
   | frame      | 0.16 | `1/(1+position)` in the stack trace |
   | recency    | 0.12 | exponential decay, 14-day half-life |
   | fault      | 0.10 | past incidents that shipped a fix touching the file |
   | churn      | 0.06 | change frequency, saturating at 20 |
   | blast      | 0.05 | fan-in, log-saturating at 50 |
   | kind       | 0.05 | a Function or Method is actionable; a File is coarse |

   Each candidate carries its `Signals` breakdown **and** a `Reasons` list in
   words, both persisted and rendered — a ranking should be auditable, not
   trusted blindly.

Recency, churn, and fault history come from the `file_stats` table: seeded from
the last 100 commits at full-index time, accrued per push thereafter, and
credited with a fault only when a resolution actually **ships** a fix touching
the file (a candidate that merely ranked highly and was innocent must not).

### Tier 3 — model fix (`internal/llm`)

Only now is a model involved, and it sees at most **three files** — the ones the
graph selected, stack-trace files first. The prompt carries the alert, the
resolved trace, and the ranked candidates with their reasons, so the model starts
from the graph's evidence instead of re-deriving it.

The response is constrained to a single strict-schema `propose_fix` tool call:
root cause, one-line summary, confidence, and a list of exact-string edits.

The provider is pluggable. Everything above the vendor boundary — the schema, the
system prompt, and the prompt assembly — lives in `internal/llm/llm.go`; each
vendor implements the same `Fixer` interface over it, so switching providers
never touches the pipeline or changes what the model is asked. On Anthropic it
runs `claude-opus-5` with adaptive thinking, streamed, with server-side refusal
fallback; on Gemini it runs `gemini-3.7-flash` with thinking on and function
calling forced to `propose_fix`. Either way a stack trace that trips a safety
classifier degrades to a documented decline rather than a stalled incident.

**Declining is a first-class outcome.** The system prompt tells the model to
return its analysis with no edits when the cause is outside the files it was
given, or when several causes fit — a precise "here is the cause and what I'd
need" beats a confident guess that has to be reverted.

### Applying and shipping (`internal/patch`, `internal/github`)

A fix is a set of **exact-string replacements**, never a unified diff. Diffs carry
line numbers and context that drift, and a miscounted hunk header applies cleanly
in the wrong place. `patch.Apply` requires each anchor to match its file **exactly
once** — zero matches means invented code, more than one means an ambiguous
anchor, and both are refused rather than guessed. A bad fix is therefore a
rejection, not a corruption.

Validated changes become one atomic commit via the Git data API (blobs → tree →
commit → ref), then a pull request whose body carries the root cause, the
resolved stack trace, the ranked graph evidence, the per-edit explanations, the
token spend, and an explicit notice that the change was **not** tested. Branch
creation is idempotent: a retry after a worker died mid-push force-updates the
ref instead of failing forever on a leftover branch.

Only a fix that actually shipped is recorded as a pattern — recording one for a
patch that never opened would teach the cache to replay it.

### What happens when a stage is unavailable

The pipeline degrades rather than failing. No model API key → tiers 1 and 2
still run and the incident lands with a ranked root-cause analysis.
`QUISAC_AUTO_PR=false` → the fix is still produced and validated, and recorded for
review, but nothing is pushed. Neo4j down → the pattern cache still resolves known
incidents. **The floor the pipeline guarantees is that an incident reaches a human
with the best analysis available**, never a silent failure.

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

**The full index is bounded, not atomic.** Parsing is parallel, the repo clear
and the inserts are batched across transactions, and external references are
never materialised. Measured over this repository, before → after: 87 ms → 17 ms,
1651 → 766 nodes, 3640 → 1024 edges, 801 KiB → 249 KiB of serialised graph.

**Remaining levers (not yet implemented):**
- The full-index result is still serialised into `jobs.graph` and shipped whole
  to the browser. It should be a summary row, with the explorer reading a
  bounded, aggregated view from Neo4j.
- `DownloadRepo` buffers the entire zipball in memory and extracts every entry,
  including the dependency directories the walk then skips.
- Structural pushes rebuild the whole `IMPORTS` edge layer (O(imports)). A token
  index (`import_keys` per file) would scope inbound recompute to true importers.
- Per-repo Neo4j databases (multi-database) for isolation at very high tenant counts.

## Known limitations
- ≥20-commit pushes fall back to full re-index (GitHub truncates the payload's
  file lists); the robust fix is the Compare API.
- Cross-file *call* edges are still not resolved; the backward walk bridges files
  through `CONTAINS` + `IMPORTS` instead, which finds the right file but not
  always the right function inside it.
- Traversal runs against every indexed repo for the installation and keeps the
  highest-scoring result. That is O(repos) Cypher round trips per incident; a
  service→repo mapping would make it O(1).
- The churn seed costs one GitHub API call per commit (bounded at 100). It is
  best-effort and never fails an index, but it does add seconds to a first index.
- A fix is validated to *apply*, never to *run*. Nothing is compiled or tested
  before the pull request opens — which is why they open as drafts by default.
```