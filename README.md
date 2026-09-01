# Quisac

Quisac turns a production alert into a pull request.

It keeps a live dependency graph of your code in Neo4j. When PagerDuty fires,
Quisac fingerprints the error, traces it back through the graph to a ranked set
of suspect symbols, and — if it is confident — opens a pull request with the fix.
Errors it has seen before are resolved from cache without a model call at all.
See [Arch.md](Arch.md) for how the three tiers work.

## Run it with Docker (recommended for a fresh clone)

Requires only Docker Desktop (or Docker Engine + compose).

```
cp .env.example .env
# Edit .env and fill in the GitHub App + session values (see below).
docker compose up --build      # or: make up
```

This starts three containers — **Postgres**, **Neo4j**, and the **app** — on one
network. The app applies its Postgres schema migrations on boot (so it also
works against managed Postgres), and Neo4j constraints too. The app waits for
both to be healthy before starting, then serves on http://localhost:8000.

Stop everything with `make down`. Data persists in the `pgdata` / `neo4jdata`
volumes; `docker compose down -v` wipes them.

### What you still have to configure in `.env`

Docker provisions the databases for you, but the app will not boot without a
GitHub App (this is app-specific and can't be shipped in the repo):

- `QUISAC_SESSION_SECRET` — any random string.
- Register a GitHub App and set `GITHUB_APP_ID`, `GITHUB_APP_SLUG`,
  `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET`, `GITHUB_WEBHOOK_SECRET`,
  `GITHUB_APP_PRIVATE_KEY`, `GITHUB_REDIRECT_URL`.
  - OAuth callback: `http://localhost:8000/github/callback`
  - Webhook URL: `<public-host>/webhook` — for local dev, expose port 8000 with
    a tunnel (e.g. ngrok) and use that host.
  - Permissions: `contents: read` and `metadata: read` are enough to index. To
    let Quisac open fixes you also need **`contents: write`** and
    **`pull_requests: write`**.

### Optional: incident resolution

Everything above gets you indexing and graph-based root-cause analysis. To have
Quisac write fixes for incidents it has not seen before, set an API key for one
of the supported providers: `ANTHROPIC_API_KEY` or `GEMINI_API_KEY`. Pick between
them with `QUISAC_LLM_PROVIDER` (`anthropic` or `gemini`); with it unset,
whichever key is present decides.

Neither path is covered by a chat subscription — a Claude Pro or Max plan grants
no Anthropic API credit. Gemini's default model (`gemini-3.5-flash`) does run on
a free tier, but Google uses free-tier requests to improve their products, and
this pipeline sends source code and production stack traces. Free-tier keys are
for trying it on your own repositories; anything handling someone else's code
wants a billed key.

Without a key, alerts are still ingested, traced through the graph, and matched
against previously-recorded fixes — novel incidents just stop at a ranked
analysis instead of a patch.

Generated pull requests open as **drafts** by default (`QUISAC_DRAFT_PR`), and
automatic opening can be turned off entirely with `QUISAC_AUTO_PR=false` — the
fix is still produced and recorded for review, but nothing is pushed. See
`.env.example` for the full set of knobs.

### PagerDuty

Point a PagerDuty V3 webhook at
`<public-host>/api/pagerduty/webhook?installation_id=<your installation id>`.
The Incidents view in the app shows the exact URL with a copy button.

Set `PAGERDUTY_WEBHOOK_SECRET` to the subscription's signing secret. Deliveries
whose signature does not match are rejected with 401. Leave it unset only in
local dev: unset, deliveries are accepted unverified, and anyone who can reach
the endpoint can spend model tokens and open pull requests.

You do **not** need to edit `DATABASE_URL` / `NEO4J_URI` for the Docker path —
compose overrides them to point at the `postgres` / `neo4j` services.

## Run it on the host (no Docker for the app)

Needs Go 1.26+, Node 20, and a C compiler (CGO / tree-sitter). Start Postgres
and Neo4j yourself, point `.env` at them, then:

```
cd html && npm install && npm run build          # build the frontend
make dev                                          # → http://localhost:8000
```

The app runs its schema migrations automatically on boot; no manual `psql` step.

## Make targets

```
make up      — build + start the full Docker stack
make down    — stop the Docker stack
make dev     — run the server on the host (databases must already be up)
make test    — run all Go tests
make deploy  — push to main and deploy to Railway
```

## Testing

`make test` runs the unit suite against fakes. The database layer also has
integration tests that exercise the real SQL; they skip unless you point them at
a scratch Postgres:

```
QUISAC_TEST_DATABASE_URL=postgres://user:pass@localhost:5432/scratch \
  go test ./internal/db/ -run Integration
```

## Neo4j docker

```
docker pull neo4j:5

docker run -d \
  --name neo4j \
  -p 7474:7474 \
  -p 7687:7687 \
  -e NEO4J_AUTH=neo4j/password123 \
  -v neo4j_data:/data \
  neo4j:5
```
