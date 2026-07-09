# Production Hardening — What Changed & Why

This document explains the changes made to move Quisac from "works on one
always-on box" to "survives restarts and runs on more than one replica." Read it
top to bottom; each section is _what changed → why → how it affects the running
process_, with a code snippet.

---

## TL;DR

| # | Change | One-line reason |
|---|--------|-----------------|
| 1 | Durable job queue + worker pool (Postgres) | Jobs no longer vanish on restart or break with >1 replica |
| 2 | Graceful shutdown | Deploys stop killing in-flight work |
| 3 | Schema migrations on boot | Schema actually exists on managed Postgres and can evolve |
| 4 | Jobs bound to a user; generic error responses | Close an info-leak / cross-user read |
| 5 | Structured logs, request IDs, `/healthz` + `/readyz`, metrics | You can operate and debug it |
| 6 | Per-user rate limit + job eviction | One user can't exhaust the box; the table can't grow forever |

**Nothing in the frontend contract changed.** `POST /api/index` still returns
`{jobId}`, and polling `GET /api/graph/{id}` still returns
`{status: "pending" | "done" | "failed"}`.

---

## The one root problem everything here fixes

Before, three pieces of state lived **in process memory or in detached
goroutines**:

- index jobs were a `map[string]*indexJob` in RAM,
- webhook pushes were handled by `go s.processPush(evt)` _after_ the HTTP
  response was already sent,
- "one push at a time per repo" was an in-process mutex.

All three assume **one process that never restarts**. On Railway with rolling
deploys and autoscaling, that assumption is false: a job created on replica A is
invisible to replica B, a deploy mid-push loses the push silently, and two
replicas happily corrupt the same repo's graph in parallel.

The fix is to move that state into Postgres, where every replica shares it.

---

## 1. Durable job queue + worker pool

### What changed
The in-memory job map and the `go processPush` goroutine are gone. Both kinds of
async work — user-triggered **full indexes** and webhook-driven **push updates**
— are now rows in a Postgres `jobs` table. The HTTP server only _enqueues_; a
separate **worker pool** claims and runs them.

```
        BEFORE                                AFTER
  ┌────────────┐                        ┌────────────┐   enqueue row
  │  handler   │ go processPush(evt) ─▶ │  handler   │ ───────────────┐
  └────────────┘  (lost on restart)     └────────────┘                ▼
                                                              ┌──────────────┐
                                                              │  jobs table  │ (Postgres)
                                                              └──────┬───────┘
                                                                     │ claim
                                                              ┌──────▼───────┐
                                                              │ worker pool  │─▶ ingest
                                                              └──────────────┘
```

### Why
- **Crash-safe.** A push is written to the DB _before_ we ack GitHub. If the
  process dies, the row is still there.
- **Horizontally scalable.** Any replica's workers can claim any job.
- **Serialized per repo, correctly.** Enforced by the database, not a
  process-local lock.

### How it works — the serialization guarantee
The key is one partial unique index (`internal/db/migrations/0002_jobs.sql`):

```sql
-- At most one running job per repo, ever — across all replicas.
CREATE UNIQUE INDEX jobs_one_running_per_repo ON jobs (repo) WHERE status = 'running';
```

Workers claim the oldest pending job whose repo isn't already running, locking
the row so two workers can't grab the same one (`internal/db/jobs.go`):

```sql
UPDATE jobs SET status = 'running', started_at = now(), attempts = attempts + 1
WHERE id = (
    SELECT j.id FROM jobs j
    WHERE j.status = 'pending'
      AND NOT EXISTS (SELECT 1 FROM jobs r WHERE r.repo = j.repo AND r.status = 'running')
    ORDER BY j.created_at
    FOR UPDATE SKIP LOCKED      -- two workers never claim the same row
    LIMIT 1
)
RETURNING ...
```

If two workers still race on the _same repo_ (different rows), the unique index
makes one `UPDATE` fail; `ClaimJob` catches that and retries. The net guarantee:
**never two running jobs for one repo**, proven by `TestClaimSerialisesPerRepo`.

### Effect on the process
- `POST /api/index` returns in milliseconds (just an INSERT), not after a 30-min
  index.
- A webhook is durably queued before GitHub gets its `204`; if the enqueue
  fails, we return 500 so **GitHub retries** instead of silently dropping it
  (`internal/server/webhook.go`):

```go
if err := s.enqueuePush(r.Context(), evt); err != nil {
    // Fail the delivery so GitHub retries; the push is not yet durable.
    http.Error(w, "could not enqueue", http.StatusInternalServerError)
    return
}
w.WriteHeader(http.StatusNoContent)
```

### Crash recovery (the reaper)
A worker that dies mid-job leaves its row stuck in `running`. The pool's
maintenance loop returns such rows to `pending` after a threshold **longer than
the job timeout**, so a legitimately long job is never reaped (`internal/worker/worker.go`):

```go
// ReapAfter must exceed JobTimeout so a legitimately long job is never reaped.
if c.ReapAfter <= 0 { c.ReapAfter = 45 * time.Minute }   // vs JobTimeout 30m
```

---

## 2. Graceful shutdown

### What changed
`cmd/quisac` no longer calls a bare `http.ListenAndServe`. It listens for
`SIGINT`/`SIGTERM`, drains HTTP connections, then waits (bounded) for workers to
finish in-flight jobs.

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
...
<-ctx.Done()                          // signal arrives
httpSrv.Shutdown(shutdownCtx)         // stop accepting, drain in-flight requests
select {
case <-poolDone:                      // workers finished
case <-time.After(shutdownGrace):     // give up; stuck jobs get reaped next boot
}
```

### Why / effect
A deploy used to hard-kill in-flight HTTP requests and every background
goroutine — with the old in-memory design, **every deploy was a data-loss
event.** Now a deploy drains cleanly, and the worst case (a job that outruns the
grace period) is safely recovered by the reaper on the next boot instead of lost.

---

## 3. Schema migrations on boot

### What changed
The schema used to be applied by mounting `schema.sql` into Postgres's
`docker-entrypoint-initdb.d` — which **only runs on a brand-new empty volume and
never runs at all on managed Postgres (Railway).** Now the app owns its schema:
numbered files in `internal/db/migrations/`, applied on boot.

```go
store, _ := db.Open(ctx, cfg.Database.URL)
if err := store.Migrate(ctx); err != nil {   // applies pending migrations
    exit("migrate", err)
}
```

`Migrate` is safe to run every boot and from several replicas at once — an
advisory lock serializes them and a `schema_migrations` ledger makes each file
apply exactly once (`internal/db/migrate.go`):

```go
conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrateLockKey)  // one migrator at a time
// ... for each *.sql not in schema_migrations: run it in a tx, record its name
```

### Effect
Fresh deploys — including managed Postgres — get the schema automatically. Schema
changes are now a new numbered file, not a manual `psql` step. The old
`schema.sql` and its docker-compose mount were removed.

---

## 4. Job ownership + no leaked errors

### What changed
Two things, both on the `/api/graph/{id}` poll path:

1. **Jobs are bound to a `user_id`.** Reading a job requires being its owner, so
   one user can't read another's result graph:

```go
// internal/db/jobs.go — the WHERE clause is the fix
SELECT status, graph, error FROM jobs
 WHERE id = $1 AND user_id = $2 AND kind = 'full_index'
```

2. **Failure responses are generic.** The detailed error is logged server-side;
   the client gets a fixed message so internal paths / driver text never leak
   (`internal/server/indexer.go`):

```go
case db.StatusFailed:
    w.WriteHeader(http.StatusInternalServerError)
    json.NewEncoder(w).Encode(map[string]string{"status": "failed", "error": "indexing failed"})
```

### Effect
Closes an IDOR-style cross-user read and an info-leak. Verified by
`TestHandleGraphScopesToUser` and `TestHandleGraphHidesInternalError`.

---

## 5. Observability: logs, request IDs, health, metrics

### What changed
- `fmt.Println` everywhere → **structured `slog` JSON logs**.
- Every request gets an `X-Request-Id` (reused from the proxy or minted), and
  every log line during that request carries it (`internal/server/middleware.go`).
- **`/healthz`** — liveness, always 200 if the process serves HTTP.
- **`/readyz`** — readiness, actually pings Postgres and Neo4j:

```go
srv.AddReadinessCheck("postgres", store.Ping)
srv.AddReadinessCheck("neo4j", graphStore.Ping)
```
```
$ curl localhost:8000/readyz
{"checks":{"neo4j":"ok","postgres":"ok"},"ready":true}   # 200, or 503 if a dep is down
```
- **`/debug/vars`** — process counters (`quisac_jobs_done`, `quisac_jobs_failed`).

### Effect
A load balancer can route around a replica that lost its database (`/readyz` →
503). When a webhook fails you get one structured, correlatable log line instead
of nothing. This is the difference between operable and not.

---

## 6. Rate limit + eviction

### What changed
- **Per-user cap** on concurrent full-index jobs (`internal/server/indexer.go`):

```go
const maxActiveIndexJobs = 3
...
if active >= maxActiveIndexJobs {
    http.Error(w, "too many in-flight index jobs; try again shortly", http.StatusTooManyRequests)
    return
}
```

- **Eviction:** the maintenance loop deletes terminal (`done`/`failed`) jobs
  older than a TTL, so the table and stored result-graphs don't grow forever.

### Effect
One user can't queue unbounded 30-minute jobs to exhaust the box; storage stays
bounded.

---

## How to run it

Nothing new operationally — the schema step is now automatic:

```bash
cp .env.example .env      # fill in the GitHub App values (unchanged)
docker compose up --build
```

On boot the app: connects to Postgres → **runs migrations** → connects to Neo4j
→ starts the HTTP server and the worker pool. It won't accept traffic until both
datastores are healthy (compose `depends_on` + `/readyz`).

Handy checks once it's up:

```bash
curl localhost:8000/healthz     # liveness
curl localhost:8000/readyz      # dependency health
curl localhost:8000/debug/vars  # job counters
```

---

## What was deliberately NOT done

**Paging the graph read path.** A finished index still returns the _entire_
graph JSON to the browser in one response, and `GraphCanvas.jsx` renders it
whole. For very large repos that is a large response and a heavy client render.
Fixing it properly means a windowed/neighborhood fetch API plus frontend changes,
which is its own project. The rate limit and eviction bound the _server-side_
cost; the big-response-to-browser case is still open and is the honest next
step.
