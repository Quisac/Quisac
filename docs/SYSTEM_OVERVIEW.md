# Quisac — Complete System Overview

This document explains **everything Quisac does**, from an empty process to a
rendered dependency graph that stays live, and **why each piece exists**. Read it
top to bottom: it follows the actual lifecycle. Reference tables at the end cover
every package and data store.

---

## 1. What Quisac is (in one paragraph)

Quisac connects to your GitHub repositories and builds a **code dependency
graph** of each one — every file, class, function, method, and the relationships
between them (imports, calls, inheritance, etc.). It builds that graph once from
a full scan, stores it in **Neo4j**, and then keeps it **live** by listening to
GitHub push webhooks and surgically updating only what changed. The graph is the
foundation for downstream "what breaks if I touch this?" features
(blast-radius, incident resolution). The UI lets you connect GitHub, pick a repo,
index it, and explore the resulting graph visually.

**Two databases, two jobs:**
- **Postgres** — who you are and what work is queued (users, sessions, jobs).
- **Neo4j** — the code graph itself (symbols and their relationships).

---

## 2. The moving parts

```
   Browser (React SPA)
        │  HTTP + cookie
        ▼
   ┌─────────────────────────────────────────────┐
   │  Go server (internal/server)                 │  transport only:
   │  routing · OAuth · webhook verify · enqueue  │  no graph logic
   └───────┬───────────────────────┬─────────────┘
           │ enqueue job            │ read/write auth
           ▼                        ▼
   ┌──────────────┐          ┌──────────────┐
   │  jobs table  │          │ users/sessions│   Postgres
   └──────┬───────┘          └──────────────┘
          │ claim
          ▼
   ┌──────────────┐   RepoSource   ┌──────────────┐
   │ worker pool  │ ─────────────▶ │   github     │  GitHub App API
   │ (internal/   │                │  (App API)   │
   │  worker)     │   GraphStore   └──────────────┘
   └──────┬───────┘ ─────────────▶ ┌──────────────┐
          │ ingest orchestration   │  graphstore  │─▶ Neo4j
          ▼                        │  (Cypher)    │
   ┌──────────────┐                └──────────────┘
   │   ingest     │
   │ FullIndex /  │  parse pipeline: filewalk → treesitter →
   │ ProcessPush  │  astparsers → core.Graph → resolver
   └──────────────┘
```

**Tech stack:** Go backend (net/http, pgx, neo4j-go-driver, tree-sitter),
React + Vite frontend (`react-force-graph-2d` for the visualization), Postgres 16,
Neo4j 5, all wired together with Docker Compose.

---

## 3. Boot sequence — what happens when the process starts

`cmd/quisac --serve` (`cmd/quisac/main.go`) does this, in order:

1. **Build the logger** — structured JSON logs via `slog`, set as the process default.
2. **Load config** (`internal/config`) — reads env vars (GitHub App creds, DB
   URLs, session secret) and **fails fast** if any required one is missing. This
   exists so misconfiguration is caught at boot, not at the first request.
3. **Open Postgres** and **run migrations** (`store.Migrate`) — applies the
   embedded SQL migrations so the schema always exists (works even on managed
   Postgres). See §12.
4. **Open Neo4j** (`internal/graphstore`) — verifies connectivity and ensures the
   `(repo, id)` uniqueness constraint that makes graph traversal fast.
5. **Build the GitHub App client** (`internal/github`) — parses the App private
   key once; this object mints per-installation tokens on demand.
6. **Start the worker pool** (`internal/worker`) — background goroutines that
   drain the job queue.
7. **Start the HTTP server** with graceful-shutdown wiring (§13).
8. Register **readiness checks** for Postgres and Neo4j.

Only after Postgres and Neo4j are healthy does it serve traffic (Compose gates
this with healthchecks).

---

## 4. Frontend loads — "am I connected?"

The browser loads the React SPA (`html/src/App.jsx`). The **very first thing** it
does is call `GET /api/me` (`html/src/useSession.js`):

```js
fetch('/api/me', { credentials: 'same-origin' })   // sends the session cookie
```

The server reads the `quisac_session` cookie, looks it up in Postgres, and
returns `{connected, login, installationId}` or 401.

- **Connected** → render the **Dashboard** (repo explorer + graph).
- **Not connected** → render the **Landing** page with a "Connect" button.

**Why it works this way:** the `HttpOnly` cookie is the single source of truth for
auth. The SPA keeps *no* client-side auth state to drift out of sync — it just
asks the backend. This is why re-opening `localhost:8000` shows "already
connected": a valid session row still exists in Postgres and the cookie still
points at it.

---

## 5. Connecting GitHub — the OAuth + App-install flow

This is the most intricate flow. It uses **two GitHub concepts on purpose**:

- **OAuth (user-to-server)** → proves *who the human is*.
- **App installation** → grants the app *access to specific repos*.

Step by step (`internal/server/server.go`, `internal/github/oauth.go`):

1. User clicks Connect → `GET /api/connect` → `startFlow`.
2. `startFlow` mints a random **state** token, **HMAC-signs** it, stores it in a
   short-lived (10-min) `quisac_oauth_state` cookie, and redirects to GitHub's
   authorize URL. *Why:* the state is a **CSRF guard** — GitHub must echo it back
   unchanged.
3. User approves on GitHub → GitHub redirects to `/github/callback?code=…&state=…`.
4. `handleCallback`:
   - Verifies the returned `state` matches the signed cookie (`validState`). Bad
     match → 400.
   - Exchanges `code` for a **user access token** (used transiently, never stored).
   - Reads the user's stable numeric id + login (`app.User`, with 401-retry
     backoff because a fresh App token can briefly 401 before it propagates).
   - Lists the user's **installations** (`app.UserInstallations`). None yet →
     redirect to GitHub's install screen. Otherwise pick the installation id.
   - Calls `startSession` (§6) and redirects to `/`.

**Why two cookies:** `quisac_oauth_state` is a throwaway CSRF nonce for the
round-trip; `quisac_session` is the durable login. Different jobs, different
lifetimes.

---

## 6. Sessions — how "logged in" is represented

`startSession` (`internal/server/server.go`) does three things:

1. `UpsertUser(id, login)` → `users` table.
2. `CreateSession` → `sessions` row `{id: random token, user_id, installation_id,
   expires_at = now + 7 days}`.
3. Sets the `quisac_session` cookie to that random token — `HttpOnly`,
   `SameSite=Lax`, `Secure` behind an HTTPS proxy.

The session cookie is **an opaque random token with no data in it**. Every authed
request runs `s.session(r)`, which looks the token up in Postgres and filters on
`expires_at > now()` (`internal/db/sessions.go`):

```sql
SELECT s.id, s.user_id, u.login, s.installation_id, s.expires_at
  FROM sessions s JOIN users u ON u.id = s.user_id
 WHERE s.id = $1 AND s.expires_at > now()
```

**Why opaque + DB-backed instead of a signed JWT:** revocation is instant (delete
the row = logout), and the cookie can't be tampered into carrying false claims
because it carries no claims at all. `POST /api/logout` deletes the row and clears
the cookie.

| Cookie | Purpose | Contents | Verified by | TTL |
|--------|---------|----------|-------------|-----|
| `quisac_oauth_state` | CSRF for OAuth round-trip | signed nonce | HMAC recompute | 10 min |
| `quisac_session` | logged-in session | opaque random token | Postgres lookup | 7 days |

---

## 7. Listing repositories

When the Dashboard mounts, `RepoExplorer.jsx` calls `GET /api/repos`. The server
(`handleRepos`) takes the session's `installation_id` and asks the GitHub App API
for every repo that installation can access (`internal/github/repos.go`,
paginated). *Why installation-scoped:* the app only ever sees repos the user
explicitly granted at install time — least privilege.

The UI shows them in a searchable rail with a per-repo status dot (idle /
pending / done / error).

---

## 8. Indexing a repo — the full parse pipeline

This is the heart of the product. When you click **Index repository**,
`RepoExplorer.jsx` does `POST /api/index {repo}`, gets back a `{jobId}`, and polls
`GET /api/graph/{jobId}` every 1.5s until `done` or `failed`.

Behind that, the server **enqueues a durable job** and a worker runs
`ingest.FullIndex`. Here's the whole pipeline:

### 8a. Download (`internal/github/repos.go` → `DownloadRepo`)
Fetches the repo as a **zip tarball** via the installation token, extracts it to a
temp dir (with zip-slip protection), and returns a cleanup function. *Why a
tarball:* one request gets the whole tree; the temp dir is parsed then deleted —
**no source is ever persisted**.

### 8b. Walk + parse (`internal/filewalk` → `ReadRepo`)
Two passes:

**Pass 1 — AST extraction.** Walk every file, skipping dependency/build dirs
(`node_modules`, `dist`, `.next`, …), minified bundles (`*.min.*`), and anything
over 1 MiB — *because those are generated blobs, not hand-written source, and
parsing them produces meaningless giant symbols.* Each supported file (`.py`,
`.go`, `.js/.jsx/.ts/.tsx`) is parsed by **tree-sitter** (`internal/treesitter`)
into a language-specific AST, then a per-language extractor
(`internal/astparsers`) pulls out symbols:

```go
type Extracted struct {
    Imports         []string     // raw import strings
    Functions       []string     // top-level function names
    Methods         []string     // "ClassName.methodName"
    Classes         []string
    Inheritance     []string     // "Child->Parent"
    GlobalVariables []string
    Calls           []CallEdge   // call sites, with the enclosing scope
    DecoratorUses   []DecoratorEdge
}
```

Each `Extracted` is folded into the in-memory graph via `graph.AddFile` (§9).

**Pass 2 — import resolution.** Now that we have the **manifest** (the set of all
file paths), resolve each file's raw import strings to concrete repo-relative
paths (`internal/resolver`) and add direct **file→file `Imports` edges**. The
resolver understands Python (`from x.y import z`, relative dots, `src/`/`app/`
roots, packages via `__init__.py`) and JS/TS (`./x`, extension probing
`.ts/.tsx/.js/.jsx`, `index` files, `@scope` packages). External deps (npm/PyPI/
stdlib) resolve to nothing and are left as file→**module** edges instead.

*Why resolve against a manifest set rather than the filesystem:* the exact same
logic works for a freshly-downloaded tarball **and** for an incremental update
where only the stored manifest is available (no working tree).

### 8c. Persist (`internal/graphstore` → `SaveGraph`)
Replaces the repo's whole subgraph in Neo4j in one transaction and records the
`head_sha` on the `Repo` node. *Why store the head SHA:* the webhook path uses it
to detect drift (§11).

### 8d. Return to the browser
The worker serializes the graph to JSON and stores it on the job row; the polling
`GET /api/graph/{jobId}` returns it, and `GraphCanvas.jsx` renders it.

---

## 9. The graph model — nodes, edges, and IDs

Defined in `internal/core/graph.go`. The graph is the shared currency between
parsing and storage.

**Node types:** `File`, `Class`, `Function`, `Method`, `Module`, `Variable`.
**Edge types:** `Imports`, `Contains`, `Calls`, `Instantiates`, `Inherits`,
`Decorates`.

**IDs encode ownership**, which is the clever bit:

```
file:PATH                     e.g. file:src/app.py
class:PATH::Name              e.g. class:src/app.py::User
func:PATH::name               e.g. func:src/app.py::main
method:PATH::Class.method
var:PATH::name
module:Name                   shared, path-less (external dep or unresolved)
```

*Why IDs encode the path:* every symbol "belongs" to a file path, so an
incremental update can delete exactly one file's symbols by `path` and re-insert
them, without touching anything else. `Module` nodes are path-less because they're
**shared** across files (many files import `react`).

Edges are deduplicated as they're added (`edgeSet`), and `RawImports` (each file's
raw import strings) is kept so the cross-file import layer can be recomputed on
change **without re-parsing other files**.

---

## 10. How the graph lives in Neo4j

Data model (`internal/graphstore/graphstore.go`):

```
(:Repo   {full_name, head_sha, indexed_at})
(:Symbol:<Type> {repo, id, name, type, path [, raw_imports]})
(a:Symbol)-[:IMPORTS|CONTAINS|CALLS|INSTANTIATES|INHERITS|DECORATES]->(b:Symbol)
```

- Every symbol carries `repo`, so **one Neo4j instance is multi-tenant** across
  all repositories; `(repo, id)` is a uniqueness constraint **and** the index
  behind traversal. This is why the graph lives in Neo4j rather than being rebuilt
  from JSON per request: traversal queries are O(neighborhood), independent of
  total graph size.
- `File.raw_imports` stores raw import strings so the import layer is
  recomputable.
- Writes are **surgical**: `SaveGraph` replaces a whole repo; `ReplaceFile` /
  `DeleteFile` touch one file's `path`; `SetImports` / `RebuildImports` manage
  file→file edges. The store also exposes `FileManifest`, `RawImports`,
  `GetHead`/`SetHead`, and `LoadGraph` (reconstruct the whole graph).

**What Neo4j does NOT store:** file contents, tarballs, blobs. Only structure
(symbols + relationships keyed by path) and raw import strings. Actual code is
fetched, parsed, and thrown away.

---

## 11. Keeping it live — webhooks and incremental updates

Once a repo is indexed, GitHub push webhooks keep it current **without ever
re-downloading the repo**.

### 11a. Receiving the webhook (`internal/server/webhook.go`)
`POST /webhook`:
1. Reads the body (size-capped at 25 MiB).
2. **Verifies the HMAC signature** (`X-Hub-Signature-256`) with constant-time
   compare. *Why:* proves the payload really came from GitHub, not an attacker.
3. For a `push` event, **durably enqueues** the change set and returns `204`. If
   the enqueue fails, it returns 500 so **GitHub retries** — the push is never
   silently lost.

Only pushes to the repo's **default branch** are processed; others are dropped
without error.

### 11b. Applying the change (`internal/ingest` → `ProcessPush`)
Serialized per repo (§14). Logic:

1. **Guard / drift check.** If the stored head ≠ the push's `before` SHA, or the
   payload may be truncated (≥20 commits — GitHub truncates the file lists), fall
   back to a **full re-index**. *Why:* those are the cases where the incremental
   change set can't be trusted.
2. Otherwise, for each changed path, fetch **only that one file** at the new SHA
   (`GetFileContent`):
   - exists → `ParseFile` → `ReplaceFile` (delete that file's subgraph, insert new).
   - gone → `DeleteFile`.
3. **Recompute the import layer by blast radius:**
   - modify-only push → re-resolve just the changed files' outbound imports
     (`SetImports`).
   - structural push (a file added/removed) → `RebuildImports` (inbound edges can
     shift when the manifest changes).
4. `SetHead` to the new SHA.

*Why this design:* the cost of an update is proportional to **what changed**, not
the repo size. Memory stays flat; the tarball is never re-fetched.

---

## 12. Async work — the durable job queue

Both full indexes (user-triggered) and push updates (webhook-triggered) are
**rows in the Postgres `jobs` table**, not in-process goroutines. The server only
enqueues; the **worker pool** (`internal/worker`) claims and runs them.

- **Crash-safe:** a job is persisted before its HTTP request is acked.
- **Scales horizontally:** any replica's workers can claim any job.
- **Serialized per repo across replicas:** a partial unique index guarantees at
  most one `running` job per repo, so concurrent pushes to one repo can't
  interleave writes.
- **Retries + eviction:** failures retry up to `max_attempts`, then go terminal;
  old terminal jobs are deleted. A **reaper** recovers jobs a crashed worker left
  `running`.
- **Rate-limited:** a user may have at most 3 full-index jobs in flight.

*(This is covered in depth in `docs/PRODUCTION_HARDENING.md`.)*

---

## 13. Displaying the graph (`html/src/components/GraphCanvas.jsx`)

The done graph is fed to `react-force-graph-2d`, a force-directed canvas:

- Nodes colored by type (File=blue, Class=red, Function=green, …); a legend maps
  colors to types.
- Hover/click highlights a node and its incident edges; click pins + zooms.
- Edge opacity varies by relationship type so `Contains` (structural noise)
  recedes and `Imports`/`Inherits` stand out.

*Why canvas + force layout:* it scales to thousands of nodes far better than DOM/SVG
and gives an intuitive "clusters = subsystems" picture.

---

## 14. Cross-cutting concerns

**Graceful shutdown** (`cmd/quisac/main.go`): on `SIGINT`/`SIGTERM` it stops
accepting connections, drains in-flight HTTP requests, then waits (bounded) for
workers to finish. So a deploy doesn't kill live work.

**Observability** (`internal/server/middleware.go`, `health.go`):
- structured `slog` logs; every request tagged with an `X-Request-Id`.
- `/healthz` — liveness (process is up).
- `/readyz` — readiness (pings Postgres + Neo4j; returns 503 if a dep is down, so
  a load balancer routes around a broken replica).
- `/debug/vars` — process counters (`quisac_jobs_done`, `quisac_jobs_failed`).

**Per-repo serialization** (`internal/ingest` keyed mutex + the DB unique index):
two guarantees, in-process and cross-replica, so one repo's graph is never
written concurrently.

---

## 15. Reference — every package and why it exists

| Package | Responsibility | Why it's separate |
|---------|---------------|-------------------|
| `cmd/quisac` | Entrypoint: wire deps, run server + workers, graceful shutdown | Composition root; the only place concrete implementations are chosen |
| `internal/server` | HTTP transport: routing, OAuth, webhook verify, enqueue, health, logs | Keeps all transport concerns out of the domain logic |
| `internal/worker` | Drains the job queue; reaps/evicts jobs | Async execution decoupled from HTTP request lifecycle |
| `internal/ingest` | Orchestrates `FullIndex` + `ProcessPush` | Domain logic that depends only on narrow interfaces, not on GitHub/Neo4j directly |
| `internal/github` | GitHub App API: download repo, file content, head SHA, OAuth, repos | Isolates the one external API; satisfies `ingest.RepoSource` |
| `internal/graphstore` | Neo4j persistence (Cypher) | Isolates the graph DB; satisfies `ingest.GraphStore` |
| `internal/core` | In-memory graph model (Node/Edge/Graph, IDs) | Shared currency between parsing and storage |
| `internal/filewalk` | `ReadRepo` (full walk) + `ParseFile` (single file) → graph | The parse-pipeline driver; the `IsIndexable` predicate lives here |
| `internal/treesitter` | tree-sitter parsing → language AST | Wraps the C parser; one place per-language grammars are chosen |
| `internal/astparsers` | Per-language symbol/edge extraction (py/js/ts/go) | Language-specific rules kept out of the generic graph code |
| `internal/resolver` | Import string → repo-relative path | Language-specific import semantics, testable in isolation |
| `internal/config` | Env config + validation | Fail-fast on misconfiguration at boot |
| `internal/db` | Postgres: users, sessions, jobs, migrations | The relational store and its schema |
| `internal/session` | HMAC signer for the OAuth state cookie | Small, focused crypto helper |

**Dependencies point inward:** `server → ingest → {core, filewalk, resolver}`, and
the interface implementations (`github`, `graphstore`) are injected at
`cmd/quisac`. `ingest` never imports `github` or `graphstore` directly — it
declares `RepoSource` and `GraphStore` interfaces and receives implementations.
*Why:* the domain logic is testable with fakes and independent of the specific
external systems.

---

## 16. Data stores at a glance

| | Postgres | Neo4j |
|--|----------|-------|
| **Holds** | users, sessions, job queue | the code graph (symbols + relationships) |
| **Why this DB** | relational, transactional, `SKIP LOCKED` queue, easy migrations | graph traversal is O(neighborhood) via the `(repo, id)` index |
| **Multi-tenant** | by `user_id` | by `repo` property on every symbol |
| **Never holds** | code | file contents / tarballs / blobs |

---

## 17. Running it

```bash
cp .env.example .env      # fill in GitHub App values + a session secret
docker compose up --build # starts Postgres, Neo4j, and the app
# → http://localhost:8000
```

The app migrates its own schema on boot, waits for both datastores to be healthy,
then serves. From there: **Connect GitHub → pick a repo → Index → explore the
graph**, and every push to that repo's default branch keeps the graph current
automatically.

---

## 18. Known limitations (honest list)

- **≥20-commit pushes** fall back to a full re-index (GitHub truncates the
  payload's file lists); the robust fix is the Compare API.
- **Removed files** can leave orphan `Module` nodes (harmless; an optional sweep
  would clean them).
- **Cross-file edges are imports only**; call edges are intra-file.
- **The graph read path isn't paged** — a finished index returns the whole graph
  to the browser at once, which is heavy for very large repos (see
  `docs/PRODUCTION_HARDENING.md` §"What was deliberately NOT done").
