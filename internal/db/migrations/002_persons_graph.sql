-- Migration 002: persons graph columns, alias uniqueness, and seed congress members

-- Add columns to persons table
ALTER TABLE persons ADD COLUMN IF NOT EXISTS state TEXT;
ALTER TABLE persons ADD COLUMN IF NOT EXISTS party TEXT;
ALTER TABLE persons ADD COLUMN IF NOT EXISTS bioguide_id TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_persons_bioguide ON persons(bioguide_id) WHERE bioguide_id IS NOT NULL;

-- Unique index on entity_aliases to prevent alias poisoning
CREATE UNIQUE INDEX IF NOT EXISTS idx_entity_aliases_unique ON entity_aliases(entity_type, LOWER(alias));

-- Functional index for alias lookups
CREATE INDEX IF NOT EXISTS idx_entity_aliases_lookup ON entity_aliases(entity_type, LOWER(alias));

-- Seed 20 initial congress members for development
INSERT INTO persons (slug, name, role, tier, branch, state, party) VALUES
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
    ('hakeem-jeffries', 'Hakeem Jeffries', 'representative', 1, 'legislative', 'NY', 'D')
ON CONFLICT (slug) DO NOTHING;
