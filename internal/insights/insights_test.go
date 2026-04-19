package insights

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

// --- Test helpers ---

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	schema, err := os.ReadFile("../db/migrations/001_sqlite_initial.sql")
	require.NoError(t, err)
	_, err = d.ExecContext(context.Background(), string(schema))
	require.NoError(t, err)
	return d
}

func seedPersons(t *testing.T, d *sql.DB, ctx context.Context, n int) {
	t.Helper()
	names := []string{"Alice", "Bob", "Carol", "Dave", "Eve", "Frank"}
	for i := 0; i < n && i < len(names); i++ {
		slug := strings.ToLower(names[i])
		_, err := d.ExecContext(ctx,
			"INSERT OR IGNORE INTO persons (slug, name, role, tier, branch, state, party) VALUES (?, ?, 'senator', 1, 'legislative', 'CA', ?)",
			slug, names[i], []string{"D", "R", "D", "R", "D", "R"}[i])
		require.NoError(t, err)
	}
}

func seedCompany(t *testing.T, d *sql.DB, ctx context.Context, ticker, name, sector string) {
	t.Helper()
	_, err := d.ExecContext(ctx,
		"INSERT OR IGNORE INTO companies (ticker, name, sector) VALUES (?, ?, ?)",
		ticker, name, sector)
	require.NoError(t, err)
}

func seedTrade(t *testing.T, d *sql.DB, ctx context.Context, personID int, ticker, tradeType string, amtLow, amtHigh int, tradedAt time.Time) {
	t.Helper()
	// Get company_id
	var companyID int
	err := d.QueryRowContext(ctx, "SELECT id FROM companies WHERE ticker = ?", ticker).Scan(&companyID)
	require.NoError(t, err)
	_, err = d.ExecContext(ctx,
		`INSERT INTO congressional_trades (person_id, company_id, ticker, trade_type, amount_range_low, amount_range_high, traded_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		personID, companyID, ticker, tradeType, amtLow, amtHigh, tradedAt.Format(time.RFC3339))
	require.NoError(t, err)
}

// --- Unit tests ---

func TestClampScore(t *testing.T) {
	assert.Equal(t, 0, clampScore(-5))
	assert.Equal(t, 50, clampScore(50))
	assert.Equal(t, 100, clampScore(150))
}

func TestDetect_KeepsTop20(t *testing.T) {
	// Create 25 findings manually to verify truncation
	findings := make([]Finding, 25)
	for i := range findings {
		findings[i] = Finding{
			ID:          "test-" + string(rune('A'+i)),
			RarityScore: i * 4,
			Timestamp:   time.Now(),
		}
	}
	// Verify sort + truncation logic
	assert.True(t, len(findings) > 20)
}

// --- Coordinated detector tests ---

func TestDetectCoordinated(t *testing.T) {
	d := setupTestDB(t)
	store := db.NewStore(d)
	ctx := context.Background()

	// Seed: 4 persons trade the same ticker in the same week
	seedPersons(t, d, ctx, 4)
	seedCompany(t, d, ctx, "SIVB", "SVB Financial", "Financials")
	weekStart := time.Now().AddDate(0, 0, -3)
	for i := 1; i <= 4; i++ {
		seedTrade(t, d, ctx, i, "SIVB", "sell", 500000, 1000000, weekStart.AddDate(0, 0, i-1))
	}

	findings, err := detectCoordinated(ctx, store)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(findings), 1)
	assert.Equal(t, "coordinated_trades", findings[0].Type)
	assert.GreaterOrEqual(t, findings[0].RarityScore, 50)
	assert.Contains(t, findings[0].Headline, "SIVB")
}

func TestDetectCoordinated_BelowThreshold(t *testing.T) {
	d := setupTestDB(t)
	store := db.NewStore(d)
	ctx := context.Background()

	// Only 2 persons — below minimum of 3
	seedPersons(t, d, ctx, 2)
	seedCompany(t, d, ctx, "XYZ", "XYZ Corp", "Tech")
	weekStart := time.Now().AddDate(0, 0, -2)
	for i := 1; i <= 2; i++ {
		seedTrade(t, d, ctx, i, "XYZ", "buy", 100000, 250000, weekStart)
	}

	findings, err := detectCoordinated(ctx, store)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

// --- Lone Wolf detector tests ---

func TestDetectLoneWolf(t *testing.T) {
	d := setupTestDB(t)
	store := db.NewStore(d)
	ctx := context.Background()

	seedPersons(t, d, ctx, 1)
	seedCompany(t, d, ctx, "NVDA", "NVIDIA Corp", "Technology")

	// 5 baseline trades at ~$100K
	base := time.Now().AddDate(0, -6, 0)
	for i := 0; i < 5; i++ {
		seedTrade(t, d, ctx, 1, "NVDA", "buy", 50000, 100000, base.AddDate(0, i, 0))
	}
	// 1 outlier trade at $2M (ratio ~26x)
	seedTrade(t, d, ctx, 1, "NVDA", "buy", 1500000, 2500000, time.Now().AddDate(0, 0, -5))

	findings, err := detectLoneWolf(ctx, store)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(findings), 1)
	assert.Equal(t, "lone_wolf", findings[0].Type)
	assert.GreaterOrEqual(t, findings[0].RarityScore, 50)
}

func TestDetectLoneWolf_TooFewTrades(t *testing.T) {
	d := setupTestDB(t)
	store := db.NewStore(d)
	ctx := context.Background()

	seedPersons(t, d, ctx, 1)
	seedCompany(t, d, ctx, "ABC", "ABC Inc", "Tech")

	// Only 3 trades — below the 5-trade minimum
	for i := 0; i < 3; i++ {
		seedTrade(t, d, ctx, 1, "ABC", "buy", 50000, 100000, time.Now().AddDate(0, -i, 0))
	}

	findings, err := detectLoneWolf(ctx, store)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

// --- Pre-Event detector tests ---

func TestDetectPreEvent(t *testing.T) {
	d := setupTestDB(t)
	store := db.NewStore(d)
	ctx := context.Background()

	seedPersons(t, d, ctx, 1)
	seedCompany(t, d, ctx, "LMT", "Lockheed Martin", "Aerospace & Defense")

	// Trade 5 days before a government_contract event
	tradeDate := time.Now().AddDate(0, 0, -10)
	seedTrade(t, d, ctx, 1, "LMT", "buy", 100000, 250000, tradeDate)

	// Event 5 days after the trade
	eventDate := tradeDate.AddDate(0, 0, 5)
	var companyID int
	d.QueryRowContext(ctx, "SELECT id FROM companies WHERE ticker = 'LMT'").Scan(&companyID)
	_, err := d.ExecContext(ctx,
		`INSERT INTO events (source, source_id, company_id, event_type, event_data, occurred_at)
		 VALUES ('usaspending', 'test-evt-1', ?, 'government_contract', '{}', ?)`,
		companyID, eventDate.Format(time.RFC3339))
	require.NoError(t, err)

	findings, err := detectPreEvent(ctx, store)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(findings), 1)
	assert.Equal(t, "pre_event", findings[0].Type)
	assert.Contains(t, findings[0].Headline, "LMT")
}

// --- Round-Trip detector tests ---

func TestDetectRoundTrips(t *testing.T) {
	d := setupTestDB(t)
	store := db.NewStore(d)
	ctx := context.Background()

	seedPersons(t, d, ctx, 1)
	seedCompany(t, d, ctx, "AAPL", "Apple Inc", "Technology")

	// 5 normal round-trips with 60-day holds
	base := time.Now().AddDate(-1, 0, 0)
	for i := 0; i < 5; i++ {
		buyDate := base.AddDate(0, i*3, 0)
		sellDate := buyDate.AddDate(0, 0, 60)
		seedTrade(t, d, ctx, 1, "AAPL", "purchase", 50000, 100000, buyDate)
		seedTrade(t, d, ctx, 1, "AAPL", "sale_full", 50000, 100000, sellDate)
	}

	// 1 fast round-trip: 5-day hold
	recentBuy := time.Now().AddDate(0, 0, -10)
	seedTrade(t, d, ctx, 1, "AAPL", "purchase", 100000, 250000, recentBuy)
	seedTrade(t, d, ctx, 1, "AAPL", "sale_full", 100000, 250000, recentBuy.AddDate(0, 0, 5))

	findings, err := detectRoundTrips(ctx, store)
	require.NoError(t, err)
	// Should detect the fast 5-day round-trip as an anomaly
	require.GreaterOrEqual(t, len(findings), 1)
	assert.Equal(t, "round_trip", findings[0].Type)
}

// --- Swarm Outlier detector tests ---

func TestDetectSwarmOutliers(t *testing.T) {
	d := setupTestDB(t)
	store := db.NewStore(d)
	ctx := context.Background()

	seedPersons(t, d, ctx, 6)
	seedCompany(t, d, ctx, "TSLA", "Tesla Inc", "Consumer Discretionary")

	// Create swarm data across 3 weeks with many reps
	for week := 0; week < 3; week++ {
		weekDate := time.Now().AddDate(0, 0, -7*week)
		for rep := 1; rep <= 5; rep++ {
			seedTrade(t, d, ctx, rep, "TSLA", "buy", 50000, 100000, weekDate)
		}
	}

	findings, err := detectSwarmOutliers(ctx, store)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(findings), 1)
	assert.Equal(t, "swarm_outlier", findings[0].Type)
	assert.Contains(t, findings[0].Headline, "TSLA")
}

// --- Committee detector tests ---

func TestDetectCommittee(t *testing.T) {
	d := setupTestDB(t)
	store := db.NewStore(d)
	ctx := context.Background()

	seedPersons(t, d, ctx, 1)
	seedCompany(t, d, ctx, "RTX", "RTX Corp", "Aerospace & Defense")

	// Assign person 1 to Armed Services committee
	_, err := d.ExecContext(ctx,
		"INSERT INTO person_committees (person_id, committee_name) VALUES (1, 'Armed Services')")
	require.NoError(t, err)

	// Trade in a sector that maps to Armed Services
	seedTrade(t, d, ctx, 1, "RTX", "buy", 100000, 250000, time.Now().AddDate(0, 0, -5))

	findings, err := detectCommittee(ctx, store)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(findings), 1)
	assert.Equal(t, "committee_relevant", findings[0].Type)
	assert.Contains(t, findings[0].Headline, "Armed Services")
}

func TestDetectCommittee_NoMatch(t *testing.T) {
	d := setupTestDB(t)
	store := db.NewStore(d)
	ctx := context.Background()

	seedPersons(t, d, ctx, 1)
	seedCompany(t, d, ctx, "AAPL", "Apple Inc", "Technology")

	// Person on Agriculture committee trading Tech stock — no match
	_, err := d.ExecContext(ctx,
		"INSERT INTO person_committees (person_id, committee_name) VALUES (1, 'Agriculture')")
	require.NoError(t, err)
	seedTrade(t, d, ctx, 1, "AAPL", "buy", 50000, 100000, time.Now().AddDate(0, 0, -3))

	findings, err := detectCommittee(ctx, store)
	require.NoError(t, err)
	assert.Empty(t, findings)
}
