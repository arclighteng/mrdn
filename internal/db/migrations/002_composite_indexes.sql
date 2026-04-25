-- 002: Composite indexes for self-join queries (CoTraderNetwork, AccountabilityInputs)
CREATE INDEX IF NOT EXISTS idx_ct_ticker_traded_person
    ON congressional_trades(ticker, traded_at, person_id);

CREATE INDEX IF NOT EXISTS idx_ct_person_ticker_traded
    ON congressional_trades(person_id, ticker, traded_at);
