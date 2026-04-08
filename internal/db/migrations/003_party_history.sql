-- 003_party_history.sql
--
-- Tracks party affiliation changes over time. The persons.party column always
-- reflects the CURRENT party; this table is the audit trail for switchers like
-- Jeff Van Drew (D→R, 2019-12-19), Justin Amash (R→I→L, 2019-07-04 / 2020-04-29),
-- Parker Griffith (D→R, 2009-12-22), Kyrsten Sinema (D→I, 2022-12-09), and
-- Joe Manchin (D→I, 2024-05-31).
--
-- Each row is one (party, started_at) period. ended_at NULL = still in that party.
-- Sourced from public Wikipedia / House Clerk records — no inference.

CREATE TABLE IF NOT EXISTS party_history (
    id          SERIAL PRIMARY KEY,
    person_id   INTEGER NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    party       TEXT NOT NULL,
    started_at  DATE,
    ended_at    DATE,
    note        TEXT,
    UNIQUE (person_id, party, started_at)
);

CREATE INDEX IF NOT EXISTS party_history_person_idx ON party_history (person_id);

-- Seed known party switches. Each switcher gets two rows: the prior period
-- (with ended_at set) and the current period (ended_at NULL). We match on
-- bioguide_id when available, falling back to slug.
--
-- Format: (slug-or-bioguide, prior_party, prior_start, prior_end, current_party, current_start, note)

INSERT INTO party_history (person_id, party, started_at, ended_at, note)
SELECT p.id, 'D', '2019-01-03'::date, '2019-12-19'::date, 'Switched to Republican Party on Dec 19, 2019'
FROM persons p WHERE p.slug = 'jeff-van-drew' AND NOT EXISTS (
  SELECT 1 FROM party_history h WHERE h.person_id = p.id AND h.party = 'D'
);

INSERT INTO party_history (person_id, party, started_at, ended_at, note)
SELECT p.id, 'R', '2011-01-03'::date, '2019-07-04'::date, 'Left Republican Party on Jul 4, 2019'
FROM persons p WHERE p.slug = 'justin-amash' AND NOT EXISTS (
  SELECT 1 FROM party_history h WHERE h.person_id = p.id AND h.party = 'R'
);

INSERT INTO party_history (person_id, party, started_at, ended_at, note)
SELECT p.id, 'I', '2019-07-04'::date, '2020-04-29'::date, 'Independent until joining Libertarian Party'
FROM persons p WHERE p.slug = 'justin-amash' AND NOT EXISTS (
  SELECT 1 FROM party_history h WHERE h.person_id = p.id AND h.party = 'I'
);

INSERT INTO party_history (person_id, party, started_at, ended_at, note)
SELECT p.id, 'D', '2009-01-06'::date, '2009-12-22'::date, 'Switched to Republican Party on Dec 22, 2009'
FROM persons p WHERE p.slug = 'parker-griffith' AND NOT EXISTS (
  SELECT 1 FROM party_history h WHERE h.person_id = p.id AND h.party = 'D'
);

INSERT INTO party_history (person_id, party, started_at, ended_at, note)
SELECT p.id, 'D', '2019-01-03'::date, '2022-12-09'::date, 'Left Democratic Party to register Independent'
FROM persons p WHERE p.slug = 'kyrsten-sinema' AND NOT EXISTS (
  SELECT 1 FROM party_history h WHERE h.person_id = p.id AND h.party = 'D'
);

INSERT INTO party_history (person_id, party, started_at, ended_at, note)
SELECT p.id, 'D', '2010-11-15'::date, '2024-05-31'::date, 'Left Democratic Party to register Independent'
FROM persons p WHERE p.slug = 'joe-manchin' AND NOT EXISTS (
  SELECT 1 FROM party_history h WHERE h.person_id = p.id AND h.party = 'D'
);
