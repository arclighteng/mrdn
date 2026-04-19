-- MRDN SQLite schema (D1)
-- Converted from Postgres migrations 001-005

CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS companies (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ticker TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    sector TEXT,
    subsector TEXT,
    naics_code TEXT,
    market_cap_bucket TEXT
);

CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source TEXT NOT NULL,
    source_id TEXT,
    company_id INTEGER REFERENCES companies(id),
    event_type TEXT NOT NULL,
    event_data TEXT NOT NULL,  -- JSON stored as text
    occurred_at TEXT NOT NULL, -- ISO 8601
    ingested_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (source, source_id)
);

CREATE TABLE IF NOT EXISTS persons (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    slug TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    role TEXT NOT NULL,
    tier INTEGER NOT NULL,
    branch TEXT,
    state TEXT,
    party TEXT,
    bioguide_id TEXT,
    linked_person_id INTEGER REFERENCES persons(id),
    linked_relationship TEXT,
    disclosure_source TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_persons_bioguide ON persons(bioguide_id) WHERE bioguide_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS congressional_trades (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER REFERENCES events(id),
    person_id INTEGER REFERENCES persons(id),
    company_id INTEGER REFERENCES companies(id),
    owner_type TEXT,
    ticker TEXT,
    trade_type TEXT,
    amount_range_low INTEGER,
    amount_range_high INTEGER,
    filed_at TEXT,  -- ISO 8601
    traded_at TEXT   -- ISO 8601
);

CREATE TABLE IF NOT EXISTS contracts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER REFERENCES events(id),
    company_id INTEGER REFERENCES companies(id),
    agency TEXT,
    amount_cents INTEGER,
    action_type TEXT,
    description TEXT,
    awarded_at TEXT
);

CREATE TABLE IF NOT EXISTS sanctions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER REFERENCES events(id),
    company_id INTEGER REFERENCES companies(id),
    entity_name TEXT,
    entity_type TEXT,
    program TEXT,
    country TEXT,
    added_at TEXT
);

-- Tariffs: array columns become junction tables
CREATE TABLE IF NOT EXISTS tariffs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER REFERENCES events(id),
    action_type TEXT,
    effective_at TEXT
);

CREATE TABLE IF NOT EXISTS tariff_hs_codes (
    tariff_id INTEGER NOT NULL REFERENCES tariffs(id) ON DELETE CASCADE,
    hs_code TEXT NOT NULL,
    PRIMARY KEY (tariff_id, hs_code)
);

CREATE TABLE IF NOT EXISTS tariff_countries (
    tariff_id INTEGER NOT NULL REFERENCES tariffs(id) ON DELETE CASCADE,
    country TEXT NOT NULL,
    PRIMARY KEY (tariff_id, country)
);

CREATE TABLE IF NOT EXISTS warn_filings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER REFERENCES events(id),
    company_id INTEGER REFERENCES companies(id),
    state TEXT,
    city TEXT,
    workers_affected INTEGER,
    layoff_date TEXT,
    filed_at TEXT
);

CREATE TABLE IF NOT EXISTS donations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER REFERENCES events(id),
    company_id INTEGER REFERENCES companies(id),
    donor_name TEXT,
    donor_type TEXT,
    donor_employer TEXT,
    recipient TEXT,
    recipient_person_id INTEGER REFERENCES persons(id),
    recipient_type TEXT,
    amount_cents INTEGER,
    donated_at TEXT
);

CREATE TABLE IF NOT EXISTS lobbying (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER REFERENCES events(id),
    client_company_id INTEGER REFERENCES companies(id),
    registrant TEXT,
    client TEXT,
    specific_issues TEXT,
    amount_cents INTEGER,
    period_start TEXT,
    period_end TEXT,
    filed_at TEXT
);

-- Court filings: parties array → junction table
CREATE TABLE IF NOT EXISTS court_filings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER REFERENCES events(id),
    company_id INTEGER REFERENCES companies(id),
    case_number TEXT,
    court TEXT,
    filing_type TEXT,
    filed_at TEXT
);

CREATE TABLE IF NOT EXISTS court_filing_parties (
    filing_id INTEGER NOT NULL REFERENCES court_filings(id) ON DELETE CASCADE,
    party_name TEXT NOT NULL,
    PRIMARY KEY (filing_id, party_name)
);

CREATE TABLE IF NOT EXISTS market_data (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    company_id INTEGER NOT NULL REFERENCES companies(id),
    source TEXT NOT NULL,
    data_type TEXT NOT NULL,
    price_cents INTEGER,
    volume INTEGER,
    change_pct REAL,
    recorded_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS insider_trades (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER REFERENCES events(id),
    company_id INTEGER REFERENCES companies(id),
    filer_name TEXT,
    filer_title TEXT,
    trade_type TEXT,
    shares INTEGER,
    price_cents INTEGER,
    filed_at TEXT,
    traded_at TEXT
);

CREATE TABLE IF NOT EXISTS person_committees (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    person_id INTEGER NOT NULL REFERENCES persons(id),
    committee_name TEXT NOT NULL,
    committee_code TEXT,
    start_date TEXT,
    end_date TEXT
);

CREATE TABLE IF NOT EXISTS company_hs_codes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    company_id INTEGER NOT NULL REFERENCES companies(id),
    hs_code TEXT NOT NULL,
    source TEXT,
    confidence REAL
);

CREATE TABLE IF NOT EXISTS score_weights (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    version INTEGER UNIQUE NOT NULL,
    weights TEXT NOT NULL,  -- JSON
    active INTEGER DEFAULT 0,
    created_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS bills (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    bill_number TEXT UNIQUE NOT NULL,
    title TEXT,
    status TEXT,
    congress INTEGER,
    introduced_at TEXT,
    last_action_at TEXT,
    source TEXT
);

CREATE TABLE IF NOT EXISTS entity_aliases (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_id INTEGER NOT NULL,
    entity_type TEXT NOT NULL,
    alias TEXT NOT NULL,
    source TEXT,
    confidence REAL,
    auto_applied INTEGER DEFAULT 0
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_entity_aliases_unique ON entity_aliases(entity_type, alias COLLATE NOCASE);

CREATE TABLE IF NOT EXISTS entity_links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    from_entity INTEGER NOT NULL,
    from_type TEXT NOT NULL,
    to_entity INTEGER NOT NULL,
    to_type TEXT NOT NULL,
    relationship TEXT NOT NULL,
    evidence_event_id INTEGER REFERENCES events(id),
    discovered_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS source_meta (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_name TEXT UNIQUE NOT NULL,
    expected_lag TEXT,
    last_successful_poll TEXT,
    last_new_data_at TEXT,
    poll_interval_seconds INTEGER,
    status TEXT DEFAULT 'healthy' CHECK (status IN ('healthy', 'degraded', 'stale', 'down')),
    last_attempt_at TEXT,
    last_http_code INTEGER,
    last_error TEXT,
    last_records INTEGER,
    last_duration_ms INTEGER
);

CREATE TABLE IF NOT EXISTS scores (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    company_id INTEGER NOT NULL REFERENCES companies(id),
    market_score REAL,
    policy_score REAL,
    insider_score REAL,
    composite_score REAL,
    weight_version INTEGER REFERENCES score_weights(version),
    computed_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS api_keys (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    key_hash TEXT UNIQUE NOT NULL,
    label TEXT,
    rate_limit INTEGER DEFAULT 600,
    created_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS party_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    person_id INTEGER NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    party TEXT NOT NULL,
    started_at TEXT,
    ended_at TEXT,
    note TEXT,
    UNIQUE (person_id, party, started_at)
);

-- Indexes (MQL performance)
CREATE INDEX IF NOT EXISTS idx_events_company_occurred ON events(company_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_events_source ON events(source);
CREATE INDEX IF NOT EXISTS idx_events_type ON events(event_type);
CREATE INDEX IF NOT EXISTS idx_market_data_company_recorded ON market_data(company_id, recorded_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_entity_links_unique ON entity_links(from_entity, from_type, to_entity, to_type, relationship);
CREATE INDEX IF NOT EXISTS idx_entity_links_from ON entity_links(from_entity, from_type);
CREATE INDEX IF NOT EXISTS idx_entity_links_to ON entity_links(to_entity, to_type);
CREATE INDEX IF NOT EXISTS idx_scores_company_computed ON scores(company_id, computed_at);
CREATE INDEX IF NOT EXISTS idx_congressional_trades_company ON congressional_trades(company_id);
CREATE INDEX IF NOT EXISTS idx_congressional_trades_traded_at ON congressional_trades(traded_at);
CREATE INDEX IF NOT EXISTS idx_congressional_trades_person ON congressional_trades(person_id);
CREATE INDEX IF NOT EXISTS idx_congressional_trades_ticker ON congressional_trades(ticker);
CREATE INDEX IF NOT EXISTS idx_person_committees_person ON person_committees(person_id);
CREATE INDEX IF NOT EXISTS idx_party_history_person ON party_history(person_id);
CREATE INDEX IF NOT EXISTS idx_contracts_awarded_at ON contracts(awarded_at);
CREATE INDEX IF NOT EXISTS idx_contracts_agency ON contracts(agency);
CREATE INDEX IF NOT EXISTS idx_donations_donated_at ON donations(donated_at);
CREATE INDEX IF NOT EXISTS idx_sanctions_country_program ON sanctions(country, program);
CREATE INDEX IF NOT EXISTS idx_warn_filings_state ON warn_filings(state, filed_at);
CREATE INDEX IF NOT EXISTS idx_lobbying_registrant ON lobbying(registrant);
CREATE INDEX IF NOT EXISTS idx_court_filings_filing_type ON court_filings(filing_type, filed_at);
CREATE INDEX IF NOT EXISTS idx_companies_sector ON companies(sector);
CREATE INDEX IF NOT EXISTS idx_tariff_countries_country ON tariff_countries(country);
CREATE INDEX IF NOT EXISTS idx_tariff_hs_codes_hs ON tariff_hs_codes(hs_code);

-- Seed default score weights
INSERT OR IGNORE INTO score_weights (version, weights, active)
VALUES (1, '{"market": 0.35, "policy": 0.40, "insider": 0.25, "market_price_trend": 0.30, "market_volume_anomaly": 0.30, "market_insider_activity": 0.40, "policy_tariff": 0.25, "policy_sanctions": 0.25, "policy_contracts": 0.25, "policy_court": 0.25, "insider_congressional": 0.40, "insider_lobbying": 0.30, "insider_donations": 0.30}', 1);

-- Seed source_meta
INSERT OR IGNORE INTO source_meta (source_name, expected_lag, poll_interval_seconds, status) VALUES
    ('polygon', '1 day', 86400, 'healthy'),
    ('finnhub', 'seconds', 0, 'healthy'),
    ('edgar_form4', 'same day', 3600, 'healthy'),
    ('ofac_sdn', 'minutes', 1800, 'healthy'),
    ('usaspending', '1-2 days', 86400, 'healthy'),
    ('federal_register', '1 hour', 3600, 'healthy'),
    ('fec', '1-7 days', 86400, 'healthy'),
    ('efds_senate', '30-45 days', 3600, 'healthy'),
    ('house_clerk_ptr', '1-30 days', 86400, 'healthy'),
    ('sec_edgar_lit', '1 day', 86400, 'healthy'),
    ('score_engine', 'on-demand', 86400, 'healthy');

-- Seed 20 initial congress members
INSERT OR IGNORE INTO persons (slug, name, role, tier, branch, state, party) VALUES
    ('nancy-pelosi', 'Nancy Pelosi', 'representative', 1, 'legislative', 'CA', 'D'),
    ('mitch-mcconnell', 'Mitch McConnell', 'senator', 1, 'legislative', 'KY', 'R'),
    ('chuck-schumer', 'Chuck Schumer', 'senator', 1, 'legislative', 'NY', 'D'),
    ('kevin-mccarthy', 'Kevin McCarthy', 'representative', 1, 'legislative', 'CA', 'R'),
    ('elizabeth-warren', 'Elizabeth Warren', 'senator', 1, 'legislative', 'MA', 'D'),
    ('ted-cruz', 'Ted Cruz', 'senator', 1, 'legislative', 'TX', 'R'),
    ('bernie-sanders', 'Bernie Sanders', 'senator', 1, 'legislative', 'VT', 'I'),
    ('aoc', 'Alexandria Ocasio-Cortez', 'representative', 1, 'legislative', 'NY', 'D'),
    ('mitt-romney', 'Mitt Romney', 'senator', 2, 'legislative', 'UT', 'R'),
    ('joe-manchin', 'Joe Manchin', 'senator', 2, 'legislative', 'WV', 'D'),
    ('dan-crenshaw', 'Dan Crenshaw', 'representative', 2, 'legislative', 'TX', 'R'),
    ('katie-porter', 'Katie Porter', 'representative', 2, 'legislative', 'CA', 'D'),
    ('josh-hawley', 'Josh Hawley', 'senator', 2, 'legislative', 'MO', 'R'),
    ('kyrsten-sinema', 'Kyrsten Sinema', 'senator', 2, 'legislative', 'AZ', 'I'),
    ('marco-rubio', 'Marco Rubio', 'senator', 1, 'legislative', 'FL', 'R'),
    ('ron-wyden', 'Ron Wyden', 'senator', 2, 'legislative', 'OR', 'D'),
    ('tommy-tuberville', 'Tommy Tuberville', 'senator', 1, 'legislative', 'AL', 'R'),
    ('mark-kelly', 'Mark Kelly', 'senator', 2, 'legislative', 'AZ', 'D'),
    ('marjorie-taylor-greene', 'Marjorie Taylor Greene', 'representative', 1, 'legislative', 'GA', 'R'),
    ('hakeem-jeffries', 'Hakeem Jeffries', 'representative', 1, 'legislative', 'NY', 'D');
