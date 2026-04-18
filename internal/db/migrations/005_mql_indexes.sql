-- Migration 005: indexes for MQL query performance

CREATE INDEX IF NOT EXISTS idx_congressional_trades_traded_at ON congressional_trades(traded_at DESC);
CREATE INDEX IF NOT EXISTS idx_congressional_trades_company_traded ON congressional_trades(company_id, traded_at DESC);
CREATE INDEX IF NOT EXISTS idx_contracts_awarded_at ON contracts(awarded_at DESC);
CREATE INDEX IF NOT EXISTS idx_contracts_agency ON contracts(agency);
CREATE INDEX IF NOT EXISTS idx_donations_donated_at ON donations(donated_at DESC);
CREATE INDEX IF NOT EXISTS idx_sanctions_country_program ON sanctions(country, program);
CREATE INDEX IF NOT EXISTS idx_warn_filings_state ON warn_filings(state, filed_at DESC);
CREATE INDEX IF NOT EXISTS idx_lobbying_registrant ON lobbying(registrant text_pattern_ops);
CREATE INDEX IF NOT EXISTS idx_court_filings_filing_type ON court_filings(filing_type, filed_at DESC);
CREATE INDEX IF NOT EXISTS idx_companies_name_pattern ON companies(name text_pattern_ops);
