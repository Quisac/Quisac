-- Stores incoming PagerDuty incidents and their traversal results.
-- Incidents arrive via the public /api/pagerduty/webhook endpoint (no auth).
-- The traversal result is written when a user clicks "Discover Root Cause".

CREATE TABLE IF NOT EXISTS incidents (
    id              TEXT        PRIMARY KEY,         -- PagerDuty incident ID
    installation_id TEXT        NOT NULL,            -- Associated GitHub installation
    pd_number       INT,
    title           TEXT        NOT NULL,
    service_name    TEXT        NOT NULL,
    urgency         TEXT        NOT NULL DEFAULT 'high',
    error_body      TEXT,                            -- stack trace / alert details
    status          TEXT        NOT NULL DEFAULT 'open'
                        CHECK (status IN ('open', 'traversing', 'traversed', 'failed')),
    repo            TEXT,                            -- populated after traversal
    entry_point     JSONB,                           -- traverse.Result.EntryPoint
    candidates      JSONB,                           -- traverse.Result.Candidates
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    traversed_at    TIMESTAMPTZ
);

-- Fetch latest incidents first.
CREATE INDEX IF NOT EXISTS incidents_received_idx ON incidents (received_at DESC);
