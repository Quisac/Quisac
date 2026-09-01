-- The resolution pipeline: pattern cache (tier 1), LLM fix (tier 2), and the
-- pull request that closes the loop. Traversal (0003) ranks candidate symbols;
-- everything here turns that ranking into a merged fix.

-- ─── jobs: a third kind of async work ────────────────────────────────────────
-- Resolution runs on the same durable queue as indexing, so a crashed worker
-- never loses an in-flight incident. Resolve jobs use a synthetic repo key
-- ("incident:<id>") so they serialise per incident without blocking the index
-- queue for the repo they traverse.
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_kind_check;
ALTER TABLE jobs ADD  CONSTRAINT jobs_kind_check
    CHECK (kind IN ('full_index', 'push', 'resolve'));

-- ─── incidents: resolution outcome ───────────────────────────────────────────
-- `status` continues to describe traversal; `resolution_status` describes the
-- fix pipeline that runs after it.
ALTER TABLE incidents
    ADD COLUMN IF NOT EXISTS fingerprint       TEXT,
    ADD COLUMN IF NOT EXISTS resolution_status TEXT NOT NULL DEFAULT 'pending',
    ADD COLUMN IF NOT EXISTS resolution_method TEXT,
    ADD COLUMN IF NOT EXISTS pattern_id        BIGINT,
    ADD COLUMN IF NOT EXISTS root_cause        TEXT,
    ADD COLUMN IF NOT EXISTS confidence        REAL,
    ADD COLUMN IF NOT EXISTS fix_summary       TEXT,
    ADD COLUMN IF NOT EXISTS fix_files         JSONB,
    ADD COLUMN IF NOT EXISTS pr_url            TEXT,
    ADD COLUMN IF NOT EXISTS pr_number         INT,
    ADD COLUMN IF NOT EXISTS branch            TEXT,
    ADD COLUMN IF NOT EXISTS input_tokens      INT  NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS output_tokens     INT  NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS resolution_error  TEXT,
    ADD COLUMN IF NOT EXISTS resolved_at       TIMESTAMPTZ;

ALTER TABLE incidents DROP CONSTRAINT IF EXISTS incidents_resolution_status_check;
ALTER TABLE incidents ADD  CONSTRAINT incidents_resolution_status_check
    CHECK (resolution_status IN ('pending', 'resolving', 'resolved', 'no_fix', 'failed'));

ALTER TABLE incidents DROP CONSTRAINT IF EXISTS incidents_resolution_method_check;
ALTER TABLE incidents ADD  CONSTRAINT incidents_resolution_method_check
    CHECK (resolution_method IS NULL OR resolution_method IN ('pattern', 'llm'));

-- Pattern lookup for a repeat incident starts from the fingerprint.
CREATE INDEX IF NOT EXISTS incidents_fingerprint_idx ON incidents (fingerprint)
    WHERE fingerprint IS NOT NULL;

-- ─── incident_patterns: tier-1 cache ─────────────────────────────────────────
-- A fix that worked once is recorded here keyed by the incident's normalised
-- error fingerprint. A later incident with the same fingerprint (or a close
-- enough token overlap) reuses the fix without calling the LLM at all.
CREATE TABLE IF NOT EXISTS incident_patterns (
    id              BIGSERIAL   PRIMARY KEY,
    installation_id TEXT        NOT NULL,
    repo            TEXT        NOT NULL,
    fingerprint     TEXT        NOT NULL,   -- sha256 of the normalised signature
    signature       TEXT        NOT NULL,   -- the normalised signature, human-readable
    tokens          TEXT[]      NOT NULL DEFAULT '{}',  -- for fuzzy (Jaccard) matching
    service_name    TEXT        NOT NULL DEFAULT '',
    root_cause      TEXT        NOT NULL,
    fix_summary     TEXT        NOT NULL DEFAULT '',
    edits           JSONB       NOT NULL,   -- []llm.Edit — the recorded fix
    times_seen      INT         NOT NULL DEFAULT 1,
    times_applied   INT         NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One pattern per fingerprint per repo; a repeat sighting bumps times_seen.
CREATE UNIQUE INDEX IF NOT EXISTS incident_patterns_key
    ON incident_patterns (installation_id, repo, fingerprint);

-- Fuzzy match scans an installation's patterns; keep that scan narrow.
CREATE INDEX IF NOT EXISTS incident_patterns_install_idx
    ON incident_patterns (installation_id, last_seen_at DESC);

-- ─── file_stats: churn, recency, and fault history ───────────────────────────
-- Traversal ranks candidates partly on how recently a file changed, how often
-- it changes, and how often it has been implicated in a past incident. Churn is
-- fed by push webhooks and seeded at full-index time; faults are recorded when
-- a resolution ships a fix touching the file.
CREATE TABLE IF NOT EXISTS file_stats (
    repo            TEXT        NOT NULL,
    path            TEXT        NOT NULL,
    change_count    INT         NOT NULL DEFAULT 0,
    last_changed_at TIMESTAMPTZ,
    fault_count     INT         NOT NULL DEFAULT 0,
    last_fault_at   TIMESTAMPTZ,
    PRIMARY KEY (repo, path)
);

-- Traversal loads every stat row for one repo in a single query.
CREATE INDEX IF NOT EXISTS file_stats_repo_idx ON file_stats (repo);
