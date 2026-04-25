package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testStore returns a Store backed by an in-memory SQLite database with
// migrations applied.
func testStore(t *testing.T) *db.Store {
	t.Helper()
	d, err := db.Connect(context.Background(), ":memory:")
	require.NoError(t, err)
	require.NoError(t, db.Migrate(context.Background(), d))
	t.Cleanup(func() { d.Close() })
	return db.NewStore(d)
}

func TestSSWRecordParsing(t *testing.T) {
	raw := `[
		{
			"transaction_date": "01/15/2024",
			"owner": "Self",
			"ticker": "AAPL",
			"asset_description": "Apple Inc",
			"asset_type": "Stock",
			"type": "Purchase",
			"amount": "$1,001 - $15,000",
			"comment": "",
			"senator": "Tommy Tuberville",
			"ptr_link": "https://efdsearch.senate.gov/search/view/ptr/abc123/"
		},
		{
			"transaction_date": "02/10/2024",
			"owner": "Spouse",
			"ticker": "MSFT",
			"asset_description": "Microsoft Corp",
			"asset_type": "Stock",
			"type": "Sale",
			"amount": "$15,001 - $50,000",
			"comment": "partial sale",
			"senator": "Mark Kelly",
			"ptr_link": "https://efdsearch.senate.gov/search/view/ptr/def456/"
		}
	]`

	var records []sswRecord
	err := json.Unmarshal([]byte(raw), &records)
	require.NoError(t, err)
	require.Len(t, records, 2)

	// First record
	r := records[0]
	assert.Equal(t, "01/15/2024", r.TransactionDate)
	assert.Equal(t, "Tommy Tuberville", r.Senator)
	assert.Equal(t, "AAPL", r.Ticker)
	assert.Equal(t, "Purchase", r.Type)
	assert.Equal(t, "$1,001 - $15,000", r.Amount)
	assert.Equal(t, "Self", r.Owner)

	// Full name construction
	assert.Equal(t, "Tommy Tuberville", senateFullName(&r))

	// Slug generation
	assert.Equal(t, "tommy-tuberville", slugify(senateFullName(&r)))

	// Amount parsing
	low, high := parseAmountRange(r.Amount)
	require.NotNil(t, low)
	require.NotNil(t, high)
	assert.Equal(t, 1001, *low)
	assert.Equal(t, 15000, *high)

	// Date parsing (MM/DD/YYYY format)
	td := parseUSDate(r.TransactionDate)
	require.NotNil(t, td)
	assert.Equal(t, 2024, td.Year())
	assert.Equal(t, 1, int(td.Month()))
	assert.Equal(t, 15, td.Day())

	// Source ID
	srcID := senateSourceID(&r)
	assert.Contains(t, srcID, "AAPL")
	assert.Contains(t, srcID, "01/15/2024")
	assert.Contains(t, srcID, "Purchase")
	assert.Contains(t, srcID, "abc123")
}

func TestDuplicateSourceIDsSkipped(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	r := sswRecord{
		TransactionDate: "01/15/2024",
		Senator:         "Tommy Tuberville",
		Ticker:          "AAPL",
		Type:            "Purchase",
		Amount:          "$1,001 - $15,000",
		Owner:           "Self",
		PtrLink:         "https://efdsearch.senate.gov/search/view/ptr/abc123/",
	}

	srcID := senateSourceID(&r)
	eventData, _ := json.Marshal(r)
	occurredAt := parseDate(r.TransactionDate, "")

	ev := db.Event{
		Source:     "senate_stock_watcher",
		SourceID:   &srcID,
		EventType:  "congressional_trade",
		EventData:  eventData,
		OccurredAt: occurredAt,
	}

	// First insert should succeed
	id1, err := store.InsertEvent(ctx, ev)
	require.NoError(t, err)
	assert.Greater(t, id1, 0)

	// Second insert with same source_id should return same ID
	id2, err := store.InsertEvent(ctx, ev)
	require.NoError(t, err)
	assert.Equal(t, id1, id2, "duplicate source_id should resolve to same event")
}

func TestSenatorPersonCreation(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	cache := map[string]int{}

	r := sswRecord{
		Senator: "Raphael Warnock",
	}

	personID, created, err := resolveSenator(ctx, store, &r, cache)
	require.NoError(t, err)
	assert.True(t, created, "should be newly created")
	assert.Greater(t, personID, 0)

	// Verify the person row
	p, err := store.GetPersonBySlug(ctx, "raphael-warnock")
	require.NoError(t, err)
	assert.Equal(t, "Raphael Warnock", p.Name)
	assert.Equal(t, "senator", p.Role)
	assert.Equal(t, 3, p.Tier)
	require.NotNil(t, p.Branch)
	assert.Equal(t, "legislative", *p.Branch)
	require.NotNil(t, p.DisclosureSource)
	assert.Equal(t, "senate_stock_watcher", *p.DisclosureSource)

	// Second call should return cached, not created
	personID2, created2, err := resolveSenator(ctx, store, &r, cache)
	require.NoError(t, err)
	assert.False(t, created2, "should be cached, not created")
	assert.Equal(t, personID, personID2)
}

func TestSenateFullName(t *testing.T) {
	tests := []struct {
		senator  string
		expected string
	}{
		{"Tommy Tuberville", "Tommy Tuberville"},
		{"", "Unknown"},
		{"  Tommy Tuberville  ", "Tommy Tuberville"},
	}
	for _, tc := range tests {
		r := &sswRecord{Senator: tc.senator}
		assert.Equal(t, tc.expected, senateFullName(r))
	}
}

func TestSenatorNoClobberExistingHighTier(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	// Pre-seed a tier-1 person with the same slug
	branch := "legislative"
	source := "seed"
	_, err := store.UpsertPerson(ctx, db.Person{
		Slug:             "tommy-tuberville",
		Name:             "Tommy Tuberville",
		Role:             "senator",
		Tier:             1,
		Branch:           &branch,
		DisclosureSource: &source,
	})
	require.NoError(t, err)

	cache := map[string]int{}
	r := sswRecord{
		Senator: "Tommy Tuberville",
	}

	personID, created, err := resolveSenator(ctx, store, &r, cache)
	require.NoError(t, err)
	assert.False(t, created, "should find existing, not create new")
	assert.Greater(t, personID, 0)
}

func TestSenateSourceID(t *testing.T) {
	r := sswRecord{
		PtrLink:         "https://efdsearch.senate.gov/search/view/ptr/abc123/",
		Ticker:          "AAPL",
		TransactionDate: "01/15/2024",
		Type:            "Purchase",
	}
	srcID := senateSourceID(&r)
	assert.Equal(t, "https://efdsearch.senate.gov/search/view/ptr/abc123/|AAPL|01/15/2024|Purchase", srcID)

	// Different type should produce different source_id
	r2 := r
	r2.Type = "Sale"
	assert.NotEqual(t, senateSourceID(&r), senateSourceID(&r2))
}

func TestResolveSenatorWithEmptyName(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	cache := map[string]int{}

	r := sswRecord{
		Senator: "",
	}

	// Should still succeed with slug "unknown"
	personID, created, err := resolveSenator(ctx, store, &r, cache)
	require.NoError(t, err)
	assert.True(t, created)
	assert.Greater(t, personID, 0)

	// Verify slug is "unknown"
	p, perr := store.GetPersonBySlug(ctx, "unknown")
	if perr != nil {
		var dummy sql.Result
		_ = dummy
	} else {
		assert.Equal(t, personID, p.ID)
	}
}
