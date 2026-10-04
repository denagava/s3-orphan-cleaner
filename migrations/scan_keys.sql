CREATE TABLE IF NOT EXISTS scan_keys (
    key         TEXT        PRIMARY KEY,
    referenced  BOOLEAN     NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_scan_keys_unreferenced
    ON scan_keys (key)
    WHERE referenced = false;
