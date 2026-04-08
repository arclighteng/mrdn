-- Enrich source_meta with per-attempt status fields so the /status page can
-- show recency, latency, HTTP error codes, and the last failure message.

ALTER TABLE source_meta ADD COLUMN IF NOT EXISTS last_attempt_at TIMESTAMPTZ;
ALTER TABLE source_meta ADD COLUMN IF NOT EXISTS last_http_code INT;
ALTER TABLE source_meta ADD COLUMN IF NOT EXISTS last_error TEXT;
ALTER TABLE source_meta ADD COLUMN IF NOT EXISTS last_records INT;
ALTER TABLE source_meta ADD COLUMN IF NOT EXISTS last_duration_ms INT;

-- Make sure the sources we actually run are seeded.
INSERT INTO source_meta (source_name, expected_lag, poll_interval_seconds, status) VALUES
    ('house_clerk_ptr', '1-30 days', 86400, 'healthy'),
    ('score_engine',    'on-demand', 86400, 'healthy')
ON CONFLICT (source_name) DO NOTHING;
