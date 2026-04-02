-- MRDN initial schema
-- All tables from spec: 2026-04-01-mrdn-design.md
-- Note: schema_migrations is created by the Go migration runner, not here.

CREATE TABLE IF NOT EXISTS companies (
    id SERIAL PRIMARY KEY,
    ticker TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    sector TEXT,
    subsector TEXT,
    naics_code TEXT,
    market_cap_bucket TEXT
);

CREATE TABLE IF NOT EXISTS events (
    id SERIAL PRIMARY KEY,
    source TEXT NOT NULL,
    source_id TEXT,
    company_id INT REFERENCES companies,
    event_type TEXT NOT NULL,
    event_data JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    ingested_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (source, source_id)
);

CREATE TABLE IF NOT EXISTS persons (
    id SERIAL PRIMARY KEY,
    slug TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    role TEXT NOT NULL,
    tier INT NOT NULL,
    branch TEXT,
    linked_person_id INT REFERENCES persons,
    linked_relationship TEXT,
    disclosure_source TEXT
);

CREATE TABLE IF NOT EXISTS congressional_trades (
    id SERIAL PRIMARY KEY,
    event_id INT REFERENCES events,
    person_id INT REFERENCES persons,
    company_id INT REFERENCES companies,
    owner_type TEXT,
    ticker TEXT,
    trade_type TEXT,
    amount_range_low INT,
    amount_range_high INT,
    filed_at TIMESTAMPTZ,
    traded_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS contracts (
    id SERIAL PRIMARY KEY,
    event_id INT REFERENCES events,
    company_id INT REFERENCES companies,
    agency TEXT,
    amount_cents BIGINT,
    action_type TEXT,
    description TEXT,
    awarded_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS sanctions (
    id SERIAL PRIMARY KEY,
    event_id INT REFERENCES events,
    company_id INT REFERENCES companies,
    entity_name TEXT,
    entity_type TEXT,
    program TEXT,
    country TEXT,
    added_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS tariffs (
    id SERIAL PRIMARY KEY,
    event_id INT REFERENCES events,
    hs_codes TEXT[],
    affected_countries TEXT[],
    action_type TEXT,
    effective_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS warn_filings (
    id SERIAL PRIMARY KEY,
    event_id INT REFERENCES events,
    company_id INT REFERENCES companies,
    state TEXT,
    city TEXT,
    workers_affected INT,
    layoff_date DATE,
    filed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS donations (
    id SERIAL PRIMARY KEY,
    event_id INT REFERENCES events,
    company_id INT REFERENCES companies,
    donor_name TEXT,
    donor_type TEXT,
    donor_employer TEXT,
    recipient TEXT,
    recipient_person_id INT REFERENCES persons,
    recipient_type TEXT,
    amount_cents BIGINT,
    donated_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS lobbying (
    id SERIAL PRIMARY KEY,
    event_id INT REFERENCES events,
    client_company_id INT REFERENCES companies,
    registrant TEXT,
    client TEXT,
    specific_issues TEXT,
    amount_cents BIGINT,
    period_start DATE,
    period_end DATE,
    filed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS court_filings (
    id SERIAL PRIMARY KEY,
    event_id INT REFERENCES events,
    company_id INT REFERENCES companies,
    case_number TEXT,
    court TEXT,
    parties TEXT[],
    filing_type TEXT,
    filed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS market_data (
    id SERIAL PRIMARY KEY,
    company_id INT REFERENCES companies NOT NULL,
    source TEXT NOT NULL,
    data_type TEXT NOT NULL,
    price_cents BIGINT,
    volume BIGINT,
    change_pct NUMERIC(8,4),
    recorded_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS insider_trades (
    id SERIAL PRIMARY KEY,
    event_id INT REFERENCES events,
    company_id INT REFERENCES companies,
    filer_name TEXT,
    filer_title TEXT,
    trade_type TEXT,
    shares INT,
    price_cents BIGINT,
    filed_at TIMESTAMPTZ,
    traded_at TIMESTAMPTZ
);

-- Supporting tables

CREATE TABLE IF NOT EXISTS person_committees (
    id SERIAL PRIMARY KEY,
    person_id INT REFERENCES persons NOT NULL,
    committee_name TEXT NOT NULL,
    committee_code TEXT,
    start_date DATE,
    end_date DATE
);

CREATE TABLE IF NOT EXISTS company_hs_codes (
    id SERIAL PRIMARY KEY,
    company_id INT REFERENCES companies NOT NULL,
    hs_code TEXT NOT NULL,
    source TEXT,
    confidence NUMERIC(3,2)
);

CREATE TABLE IF NOT EXISTS score_weights (
    id SERIAL PRIMARY KEY,
    version INT UNIQUE NOT NULL,
    weights JSONB NOT NULL,
    active BOOLEAN DEFAULT false,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS bills (
    id SERIAL PRIMARY KEY,
    bill_number TEXT UNIQUE NOT NULL,
    title TEXT,
    status TEXT,
    congress INT,
    introduced_at DATE,
    last_action_at DATE,
    source TEXT
);

-- Entity resolution

CREATE TABLE IF NOT EXISTS entity_aliases (
    id SERIAL PRIMARY KEY,
    entity_id INT NOT NULL,
    entity_type TEXT NOT NULL,
    alias TEXT NOT NULL,
    source TEXT,
    confidence NUMERIC(3,2),
    auto_applied BOOLEAN DEFAULT false
);

CREATE TABLE IF NOT EXISTS entity_links (
    id SERIAL PRIMARY KEY,
    from_entity INT NOT NULL,
    from_type TEXT NOT NULL,
    to_entity INT NOT NULL,
    to_type TEXT NOT NULL,
    relationship TEXT NOT NULL,
    evidence_event_id INT REFERENCES events,
    discovered_at TIMESTAMPTZ DEFAULT NOW()
);

-- Freshness tracking

CREATE TABLE IF NOT EXISTS source_meta (
    id SERIAL PRIMARY KEY,
    source_name TEXT UNIQUE NOT NULL,
    expected_lag TEXT,
    last_successful_poll TIMESTAMPTZ,
    last_new_data_at TIMESTAMPTZ,
    poll_interval_seconds INT,
    status TEXT DEFAULT 'healthy' CHECK (status IN ('healthy', 'degraded', 'stale', 'down'))
);

-- Scores

CREATE TABLE IF NOT EXISTS scores (
    id SERIAL PRIMARY KEY,
    company_id INT REFERENCES companies NOT NULL,
    market_score NUMERIC(5,2),
    policy_score NUMERIC(5,2),
    insider_score NUMERIC(5,2),
    composite_score NUMERIC(5,2),
    weight_version INT REFERENCES score_weights(version),
    computed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- API keys

CREATE TABLE IF NOT EXISTS api_keys (
    id SERIAL PRIMARY KEY,
    key_hash TEXT UNIQUE NOT NULL,
    label TEXT,
    rate_limit INT DEFAULT 600,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Critical indexes

CREATE INDEX IF NOT EXISTS idx_events_company_occurred ON events(company_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_market_data_company_recorded ON market_data(company_id, recorded_at);
CREATE INDEX IF NOT EXISTS idx_entity_links_from ON entity_links(from_entity, from_type);
CREATE INDEX IF NOT EXISTS idx_entity_links_to ON entity_links(to_entity, to_type);
CREATE INDEX IF NOT EXISTS idx_scores_company_computed ON scores(company_id, computed_at);
CREATE INDEX IF NOT EXISTS idx_congressional_trades_company ON congressional_trades(company_id);
CREATE INDEX IF NOT EXISTS idx_person_committees_person ON person_committees(person_id);

-- Seed default score weights (v1)

INSERT INTO score_weights (version, weights, active)
VALUES (1, '{"market": 0.35, "policy": 0.40, "insider": 0.25, "market_price_trend": 0.30, "market_volume_anomaly": 0.30, "market_insider_activity": 0.40, "policy_tariff": 0.25, "policy_sanctions": 0.25, "policy_contracts": 0.25, "policy_court": 0.25, "insider_congressional": 0.40, "insider_lobbying": 0.30, "insider_donations": 0.30}', true)
ON CONFLICT (version) DO NOTHING;

-- Seed source_meta for launch sources

INSERT INTO source_meta (source_name, expected_lag, poll_interval_seconds, status) VALUES
    ('polygon', '1 day', 86400, 'healthy'),
    ('finnhub', 'seconds', 0, 'healthy'),
    ('edgar_form4', 'same day', 3600, 'healthy'),
    ('ofac_sdn', 'minutes', 1800, 'healthy'),
    ('usaspending', '1-2 days', 86400, 'healthy'),
    ('federal_register', '1 hour', 3600, 'healthy'),
    ('fec', '1-7 days', 86400, 'healthy'),
    ('efds_senate', '30-45 days', 3600, 'healthy')
ON CONFLICT (source_name) DO NOTHING;
