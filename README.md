# Quisac

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

## Ne4j docker

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
