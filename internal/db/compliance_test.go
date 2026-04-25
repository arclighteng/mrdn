package db_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mustInsertPerson inserts a minimal person row and returns its ID.
func mustInsertPerson(t *testing.T, store *db.Store, ctx context.Context, slug, name string) int {
	t.Helper()
	p, err := store.UpsertPerson(ctx, db.Person{
		Slug: slug, Name: name, Role: "senator", Tier: 1,
	})
	require.NoError(t, err)
	return p.ID
}

// mustInsertCompany inserts a company and returns its ID.
func mustInsertCompany(t *testing.T, store *db.Store, ctx context.Context, ticker, sector string) int {
	t.Helper()
	c, err := store.UpsertCompany(ctx, db.Company{
		Ticker: ticker,
		Name:   ticker + "-Corp",
		Sector: db.StrPtr(sector),
	})
	require.NoError(t, err)
	return c.ID
}

// timePtr parses an ISO-8601 date string and returns a *time.Time.
func timePtr(s string) *time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic("timePtr: invalid date: " + s)
	}
	return &t
}

// TestAccountabilityInputs exercises all four observable behaviours of the
// 6-CTE query inside AccountabilityInputs using an in-memory SQLite database.
func TestAccountabilityInputs(t *testing.T) {
	ctx := context.Background()

	t.Run("zero late trades produces late_pct=0", func(t *testing.T) {
		// One DB so seed and query share the same connection.
		rawDB := testDB(t)
		store := db.NewStore(rawDB)

		personID := mustInsertPerson(t, store, ctx, "acc-zero-late", "Zero Late Legislator")

		// Three trades filed promptly (within 45 days).
		for i, dates := range [][2]string{
			{"2023-01-01", "2023-01-10"},
			{"2023-02-01", "2023-02-15"},
			{"2023-03-01", "2023-03-20"},
		} {
			err := store.InsertCongressionalTrade(ctx, db.CongressionalTrade{
				PersonID:  db.IntPtr(personID),
				Ticker:    db.StrPtr("AAPL"),
				TradeType: db.StrPtr("Purchase"),
				TradedAt:  timePtr(dates[0]),
				FiledAt:   timePtr(dates[1]),
			})
			require.NoError(t, err, "trade %d", i)
		}

		rows, err := store.AccountabilityInputs(ctx, 1)
		require.NoError(t, err)

		var found *db.AccountabilityRow
		for i := range rows {
			if rows[i].PersonID == personID {
				found = &rows[i]
				break
			}
		}
		require.NotNil(t, found, "person should appear in results")
		assert.Equal(t, 3, found.TradeCount)
		assert.InDelta(t, 0.0, found.LatePct, 0.001, "no late trades expected")
	})

	t.Run("late trade produces late_pct > 0", func(t *testing.T) {
		rawDB := testDB(t)
		store := db.NewStore(rawDB)

		personID := mustInsertPerson(t, store, ctx, "acc-late", "Late Filer")

		// One trade filed 60 days after the transaction (> 45 day STOCK Act limit).
		require.NoError(t, store.InsertCongressionalTrade(ctx, db.CongressionalTrade{
			PersonID:  db.IntPtr(personID),
			Ticker:    db.StrPtr("MSFT"),
			TradeType: db.StrPtr("Purchase"),
			TradedAt:  timePtr("2023-05-01"),
			FiledAt:   timePtr("2023-06-30"), // 60 days later
		}))

		rows, err := store.AccountabilityInputs(ctx, 1)
		require.NoError(t, err)

		var found *db.AccountabilityRow
		for i := range rows {
			if rows[i].PersonID == personID {
				found = &rows[i]
				break
			}
		}
		require.NotNil(t, found)
		assert.InDelta(t, 1.0, found.LatePct, 0.001, "single late trade → late_pct should be 1.0")
	})

	t.Run("buy followed by sell within 60 days counts as round trip", func(t *testing.T) {
		rawDB := testDB(t)
		store := db.NewStore(rawDB)

		personID := mustInsertPerson(t, store, ctx, "acc-roundtrip", "Round Tripper")

		// Buy then sell the same ticker within 60 days.
		require.NoError(t, store.InsertCongressionalTrade(ctx, db.CongressionalTrade{
			PersonID:  db.IntPtr(personID),
			Ticker:    db.StrPtr("NVDA"),
			TradeType: db.StrPtr("Purchase"),
			TradedAt:  timePtr("2023-07-01"),
			FiledAt:   timePtr("2023-07-10"),
		}))
		require.NoError(t, store.InsertCongressionalTrade(ctx, db.CongressionalTrade{
			PersonID:  db.IntPtr(personID),
			Ticker:    db.StrPtr("NVDA"),
			TradeType: db.StrPtr("Sale (Full)"),
			TradedAt:  timePtr("2023-08-15"), // 45 days after buy — within 60-day window
			FiledAt:   timePtr("2023-08-20"),
		}))

		rows, err := store.AccountabilityInputs(ctx, 1)
		require.NoError(t, err)

		var found *db.AccountabilityRow
		for i := range rows {
			if rows[i].PersonID == personID {
				found = &rows[i]
				break
			}
		}
		require.NotNil(t, found)
		assert.Equal(t, 1, found.RoundTripCount, "one buy+sell pair within 60 days")
	})

	t.Run("trade within 14 days before qualifying event counts as pre_event", func(t *testing.T) {
		rawDB := testDB(t)
		store := db.NewStore(rawDB)

		personID := mustInsertPerson(t, store, ctx, "acc-preevent", "Pre-Event Trader")
		companyID := mustInsertCompany(t, store, ctx, "LOCKHEED", "Industrials")

		// Insert the trade 10 days before the event.
		tradeDate := "2023-09-01"
		eventDate := "2023-09-11" // 10 days after trade — within 1..14 window

		require.NoError(t, store.InsertCongressionalTrade(ctx, db.CongressionalTrade{
			PersonID:  db.IntPtr(personID),
			Ticker:    db.StrPtr("LOCKHEED"),
			TradeType: db.StrPtr("Purchase"),
			TradedAt:  timePtr(tradeDate),
			FiledAt:   timePtr("2023-09-05"),
		}))

		// Insert a qualifying event linked to the company.
		srcID := "preevent-test-001"
		occurredAt, err := time.Parse("2006-01-02", eventDate)
		require.NoError(t, err)
		_, err = store.InsertEvent(ctx, db.Event{
			Source:    "test",
			SourceID:  &srcID,
			CompanyID: &companyID,
			EventType: "government_contract",
			EventData: json.RawMessage(`{}`),
			OccurredAt: occurredAt,
		})
		require.NoError(t, err)

		rows, err := store.AccountabilityInputs(ctx, 1)
		require.NoError(t, err)

		var found *db.AccountabilityRow
		for i := range rows {
			if rows[i].PersonID == personID {
				found = &rows[i]
				break
			}
		}
		require.NotNil(t, found)
		assert.Equal(t, 1, found.PreEventCount, "trade 10 days before qualifying event")
	})

	t.Run("person below minTrades threshold is excluded", func(t *testing.T) {
		rawDB := testDB(t)
		store := db.NewStore(rawDB)

		personID := mustInsertPerson(t, store, ctx, "acc-below-min", "Too Few Trades")

		// Only 1 trade, but minTrades is 5.
		require.NoError(t, store.InsertCongressionalTrade(ctx, db.CongressionalTrade{
			PersonID:  db.IntPtr(personID),
			Ticker:    db.StrPtr("TSLA"),
			TradeType: db.StrPtr("Purchase"),
			TradedAt:  timePtr("2023-01-01"),
			FiledAt:   timePtr("2023-01-10"),
		}))

		rows, err := store.AccountabilityInputs(ctx, 5)
		require.NoError(t, err)

		for _, r := range rows {
			assert.NotEqual(t, personID, r.PersonID,
				"person with only 1 trade should not appear when minTrades=5")
		}
	})

	t.Run("committee trade overlap increments committee_trade_count", func(t *testing.T) {
		rawDB := testDB(t)
		store := db.NewStore(rawDB)

		personID := mustInsertPerson(t, store, ctx, "acc-committee", "Committee Trader")
		companyID := mustInsertCompany(t, store, ctx, "RTHEON", "Industrials")

		// Assign the person to Armed Services committee.
		_, err := rawDB.ExecContext(ctx,
			`INSERT INTO person_committees (person_id, committee_name) VALUES (?, ?)`,
			personID, "Armed Services Committee")
		require.NoError(t, err)

		// Trade in a Defense-sector company while on the Armed Services committee.
		require.NoError(t, store.InsertCongressionalTrade(ctx, db.CongressionalTrade{
			PersonID:  db.IntPtr(personID),
			Ticker:    db.StrPtr("RTHEON"),
			TradeType: db.StrPtr("Purchase"),
			TradedAt:  timePtr("2023-10-01"),
			FiledAt:   timePtr("2023-10-10"),
		}))

		// Update the trade's company_id so the committee JOIN resolves correctly.
		_, err = rawDB.ExecContext(ctx,
			`UPDATE congressional_trades SET company_id = ? WHERE person_id = ? AND ticker = ?`,
			companyID, personID, "RTHEON")
		require.NoError(t, err)

		// Update the company sector to Defense — the query uses c.sector IN ('Industrials', 'Defense').
		// 'Industrials' is already set; verify via query result.
		rows, err := store.AccountabilityInputs(ctx, 1)
		require.NoError(t, err)

		var found *db.AccountabilityRow
		for i := range rows {
			if rows[i].PersonID == personID {
				found = &rows[i]
				break
			}
		}
		require.NotNil(t, found)
		assert.Equal(t, 1, found.CommitteeTradeCount,
			"trade in Industrials sector while on Armed Services committee")
	})
}
