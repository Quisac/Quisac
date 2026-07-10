-- Durable async work queue. Replaces the in-memory job map so that jobs survive
-- restarts and can be claimed by any replica.
--
-- Two kinds of work share one table:
--   full_index — user-initiated; the browser polls by id and reads `graph`.
--   push       — webhook-driven incremental update; no result to return.
--
-- Serialisation per repo is enforced by `jobs_one_running_per_repo`: at most one
-- job per repo may be in the `running` state at a time, so concurrent pushes to
-- the same repo can never interleave writes — across replicas, not just within
-- one process.

CREATE TABLE IF NOT EXISTS jobs (
    id              TEXT        PRIMARY KEY,                     -- opaque token; also the poll id
    kind            TEXT        NOT NULL CHECK (kind IN ('full_index', 'push')),
    repo            TEXT        NOT NULL,                        -- owner/name; the serialisation key
    installation_id TEXT        NOT NULL,
    user_id         BIGINT      REFERENCES users (id) ON DELETE CASCADE,  -- NULL for webhook pushes
    payload         JSONB,                                      -- kind-specific input
    status          TEXT        NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'running', 'done', 'failed')),
    attempts        INT         NOT NULL DEFAULT 0,
    max_attempts    INT         NOT NULL DEFAULT 5,
    graph           JSONB,                                      -- full_index result, when done
    error           TEXT,                                       -- last error, when failed
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ
);

-- Claim scan: oldest pending job first.
CREATE INDEX IF NOT EXISTS jobs_pending_idx ON jobs (created_at) WHERE status = 'pending';

-- Poll and per-user rate limiting.
CREATE INDEX IF NOT EXISTS jobs_user_idx ON jobs (user_id);

-- Reaper scan for stale running jobs.
CREATE INDEX IF NOT EXISTS jobs_running_idx ON jobs (started_at) WHERE status = 'running';

-- The serialisation guarantee: at most one running job per repo.
CREATE UNIQUE INDEX IF NOT EXISTS jobs_one_running_per_repo ON jobs (repo) WHERE status = 'running';
