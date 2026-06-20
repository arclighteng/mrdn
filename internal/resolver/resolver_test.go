package resolver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// mockStore — test double for ResolverStore
// ---------------------------------------------------------------------------

type mockStore struct {
	mu sync.Mutex

	companies    []db.CompanyLookup
	listErr      error
	upsertResult db.Company
	upsertErr    error
	upsertCalls  int

	searchResult *db.CompanyLookup
	searchErr    error

	updateErr   error
	updateCalls int

	insertedMarket    []db.MarketDataRow
	insertedTrades    []db.InsiderTrade
	insertedDonations []db.Donation
	insertedContracts []db.Contract
	insertedSanctions    []db.Sanction
	insertedWarnFilings  []db.WarnFiling

	insertedCongTrades   []db.CongressionalTrade
	insertedCourtFilings []db.CourtFiling
	insertedTariffs      []db.Tariff

	insertedAliases []db.EntityAlias

	aliasByName map[string]db.CompanyLookup
	aliasErr    error

	personBySlug map[string]db.Person
	personErr    error

	// unresolvedBatches is consumed in order; each call pops the first slice.
	unresolvedBatches [][]db.Event
	unresolvedBatch   int
	unresolvedErr     error
}

func (m *mockStore) ListAllCompanyLookups(_ context.Context) ([]db.CompanyLookup, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.companies, nil
}

func (m *mockStore) UpsertCompany(_ context.Context, c db.Company) (db.Company, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.upsertCalls++
	if m.upsertErr != nil {
		return db.Company{}, m.upsertErr
	}
	if m.upsertResult.ID == 0 {
		// Return a sensible default if the caller didn't set one.
		return db.Company{ID: 99, Ticker: c.Ticker, Name: c.Name}, nil
	}
	return m.upsertResult, nil
}

func (m *mockStore) EnsureCompany(_ context.Context, c db.Company) (db.Company, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.upsertCalls++
	if m.upsertErr != nil {
		return db.Company{}, m.upsertErr
	}
	if m.upsertResult.ID == 0 {
		return db.Company{ID: 99, Ticker: c.Ticker, Name: c.Name}, nil
	}
	return m.upsertResult, nil
}

func (m *mockStore) UpdateEventCompanyID(_ context.Context, _ int, _ int) error {
	m.mu.Lock()
	m.updateCalls++
	m.mu.Unlock()
	return m.updateErr
}

func (m *mockStore) SearchCompanyByName(_ context.Context, _ string) (*db.CompanyLookup, error) {
	return m.searchResult, m.searchErr
}

func (m *mockStore) InsertMarketData(_ context.Context, row db.MarketDataRow) error {
	m.mu.Lock()
	m.insertedMarket = append(m.insertedMarket, row)
	m.mu.Unlock()
	return nil
}

func (m *mockStore) InsertInsiderTrade(_ context.Context, t db.InsiderTrade) error {
	m.mu.Lock()
	m.insertedTrades = append(m.insertedTrades, t)
	m.mu.Unlock()
	return nil
}

func (m *mockStore) InsertDonation(_ context.Context, d db.Donation) error {
	m.mu.Lock()
	m.insertedDonations = append(m.insertedDonations, d)
	m.mu.Unlock()
	return nil
}

func (m *mockStore) InsertContract(_ context.Context, c db.Contract) error {
	m.mu.Lock()
	m.insertedContracts = append(m.insertedContracts, c)
	m.mu.Unlock()
	return nil
}

func (m *mockStore) InsertSanction(_ context.Context, s db.Sanction) error {
	m.mu.Lock()
	m.insertedSanctions = append(m.insertedSanctions, s)
	m.mu.Unlock()
	return nil
}

func (m *mockStore) InsertWarnFiling(_ context.Context, w db.WarnFiling) error {
	m.mu.Lock()
	m.insertedWarnFilings = append(m.insertedWarnFilings, w)
	m.mu.Unlock()
	return nil
}

func (m *mockStore) InsertCongressionalTrade(_ context.Context, t db.CongressionalTrade) error {
	m.mu.Lock()
	m.insertedCongTrades = append(m.insertedCongTrades, t)
	m.mu.Unlock()
	return nil
}

func (m *mockStore) InsertCourtFiling(_ context.Context, cf db.CourtFiling) error {
	m.mu.Lock()
	m.insertedCourtFilings = append(m.insertedCourtFilings, cf)
	m.mu.Unlock()
	return nil
}

func (m *mockStore) InsertTariff(_ context.Context, t db.Tariff) error {
	m.mu.Lock()
	m.insertedTariffs = append(m.insertedTariffs, t)
	m.mu.Unlock()
	return nil
}

func (m *mockStore) GetCompanyByAlias(_ context.Context, alias string) (db.CompanyLookup, error) {
	if m.aliasErr != nil {
		return db.CompanyLookup{}, m.aliasErr
	}
	if m.aliasByName != nil {
		if c, ok := m.aliasByName[strings.ToLower(alias)]; ok {
			return c, nil
		}
	}
	return db.CompanyLookup{}, fmt.Errorf("getting company by alias %q: %w", alias, sql.ErrNoRows)
}

func (m *mockStore) InsertEntityAlias(_ context.Context, a db.EntityAlias) (db.EntityAlias, error) {
	m.mu.Lock()
	m.insertedAliases = append(m.insertedAliases, a)
	m.mu.Unlock()
	a.ID = len(m.insertedAliases)
	return a, nil
}

func (m *mockStore) GetPersonBySlug(_ context.Context, slug string) (db.Person, error) {
	if m.personErr != nil {
		return db.Person{}, m.personErr
	}
	if m.personBySlug != nil {
		if p, ok := m.personBySlug[slug]; ok {
			return p, nil
		}
	}
	return db.Person{}, fmt.Errorf("getting person %s: %w", slug, sql.ErrNoRows)
}

func (m *mockStore) UpsertPerson(_ context.Context, p db.Person) (db.Person, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.personBySlug == nil {
		m.personBySlug = map[string]db.Person{}
	}
	p.ID = len(m.personBySlug) + 1000
	m.personBySlug[p.Slug] = p
	return p, nil
}

func (m *mockStore) ListUnresolvedEventsAfter(_ context.Context, _ string, _ int, _ int) ([]db.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.unresolvedErr != nil {
		return nil, m.unresolvedErr
	}
	if m.unresolvedBatch >= len(m.unresolvedBatches) {
		return nil, nil
	}
	batch := m.unresolvedBatches[m.unresolvedBatch]
	m.unresolvedBatch++
	return batch, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newTestResolver builds a Resolver backed by the given mock, bypassing the
// DB round-trip that New() would normally do.
func newTestResolver(t *testing.T, st *mockStore) *Resolver {
	t.Helper()
	r, err := New(context.Background(), st)
	require.NoError(t, err)
	return r
}

// mustMarshal encodes v to JSON and panics on error.
func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func makeEvent(source string, id int, data json.RawMessage) db.Event {
	return db.Event{
		ID:         id,
		Source:     source,
		EventData:  data,
		OccurredAt: time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC),
	}
}

// ---------------------------------------------------------------------------
// normalizeName — table-driven
// ---------------------------------------------------------------------------

func TestNormalizeName(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"apple inc.", "apple"},
		{"apple inc", "apple"},
		{"apple corporation", "apple"},
		{"apple corp.", "apple"},
		{"apple corp", "apple"},
		{"microsoft limited", "microsoft"},
		{"microsoft ltd.", "microsoft"},
		{"microsoft ltd", "microsoft"},
		{"tesla holdings", "tesla"},
		{"nvidia group", "nvidia"},
		{"bp plc", "bp"},
		{"alphabet class a", "alphabet"},
		{"already clean", "already clean"},
		{"", ""},
		// multi-suffix: normalizeName makes a single pass over the suffix list.
		// "apple inc. corp." strips " corp." → "apple inc." (only one suffix per run).
		{"apple inc. corp.", "apple inc"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			got := normalizeName(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

// whitespace-only input (the function itself receives TrimSpaced input from callers)
func TestNormalizeName_Whitespace(t *testing.T) {
	// normalizeName does TrimSpace at the end so pure-space trims to "".
	got := normalizeName("   ")
	assert.Equal(t, "", got)
}

// ---------------------------------------------------------------------------
// stripCIK
// ---------------------------------------------------------------------------

func TestStripCIK(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"Apple Inc. (CIK 0000320193)", "Apple Inc."},
		{"John Smith", "John Smith"},
		{"", ""},
		{"Some Corp (CIK 1234567)", "Some Corp"},
		// Leading/trailing spaces after strip.
		{"  Widget Co. (CIK 999)  ", "Widget Co."},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.want, stripCIK(tc.input))
		})
	}
}

// ---------------------------------------------------------------------------
// strPtr
// ---------------------------------------------------------------------------

func TestStrPtr(t *testing.T) {
	t.Run("non-empty returns pointer", func(t *testing.T) {
		p := strPtr("hello")
		require.NotNil(t, p)
		assert.Equal(t, "hello", *p)
	})

	t.Run("empty returns nil", func(t *testing.T) {
		assert.Nil(t, strPtr(""))
	})
}

// ---------------------------------------------------------------------------
// resolvePolygon
// ---------------------------------------------------------------------------

func TestResolvePolygon(t *testing.T) {
	t.Run("valid bar with ticker — inserts market_data", func(t *testing.T) {
		st := &mockStore{
			companies: []db.CompanyLookup{
				{ID: 7, Ticker: "AAPL", Name: "Apple Inc"},
			},
		}
		r := newTestResolver(t, st)

		data := mustMarshal(polygonBar{
			Ticker: "AAPL", Open: 150.0, High: 155.0, Low: 149.0, Close: 152.5, Volume: 1000000,
			Timestamp: time.Now().UnixMilli(),
		})
		cid, err := r.resolvePolygon(context.Background(), makeEvent("polygon", 1, data))

		require.NoError(t, err)
		assert.Equal(t, 7, cid)
		require.Len(t, st.insertedMarket, 1)
		md := st.insertedMarket[0]
		assert.Equal(t, 7, md.CompanyID)
		assert.Equal(t, "polygon", md.Source)
		assert.Equal(t, "daily", md.DataType)
		assert.Equal(t, int64(15250), *md.PriceCents) // 152.50 * 100
		assert.Equal(t, int64(1000000), *md.Volume)
		require.NotNil(t, md.ChangePct)
	})

	t.Run("empty ticker returns 0", func(t *testing.T) {
		st := &mockStore{}
		r := newTestResolver(t, st)

		data := mustMarshal(polygonBar{Ticker: "", Close: 100.0})
		cid, err := r.resolvePolygon(context.Background(), makeEvent("polygon", 2, data))

		require.NoError(t, err)
		assert.Equal(t, 0, cid)
		assert.Empty(t, st.insertedMarket)
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		st := &mockStore{}
		r := newTestResolver(t, st)

		cid, err := r.resolvePolygon(context.Background(), makeEvent("polygon", 3, json.RawMessage(`not json`)))

		assert.Error(t, err)
		assert.Equal(t, 0, cid)
	})

	t.Run("zero open — no changePct computed", func(t *testing.T) {
		st := &mockStore{
			companies: []db.CompanyLookup{{ID: 5, Ticker: "TSLA", Name: "Tesla"}},
		}
		r := newTestResolver(t, st)

		data := mustMarshal(polygonBar{Ticker: "TSLA", Open: 0, Close: 200.0})
		cid, err := r.resolvePolygon(context.Background(), makeEvent("polygon", 4, data))

		require.NoError(t, err)
		assert.Equal(t, 5, cid)
		require.Len(t, st.insertedMarket, 1)
		assert.Nil(t, st.insertedMarket[0].ChangePct)
	})
}

// ---------------------------------------------------------------------------
// resolveEdgar
// ---------------------------------------------------------------------------

func TestResolveEdgar(t *testing.T) {
	appleCompany := db.CompanyLookup{ID: 10, Ticker: "AAPL", Name: "Apple Inc"}

	t.Run("two display names — uses second as company name", func(t *testing.T) {
		st := &mockStore{
			companies: []db.CompanyLookup{appleCompany},
		}
		r := newTestResolver(t, st)

		data := mustMarshal(edgarFiling{
			DisplayNames: []string{"John Doe (CIK 0001)", "Apple Inc (CIK 0000320193)"},
			FormType:     "4",
			FileDate:     "2025-03-01",
		})
		cid, err := r.resolveEdgar(context.Background(), makeEvent("edgar_form4", 1, data))

		require.NoError(t, err)
		assert.Equal(t, 10, cid)
		require.Len(t, st.insertedTrades, 1)
		it := st.insertedTrades[0]
		require.NotNil(t, it.FilerName)
		assert.Equal(t, "John Doe", *it.FilerName)
	})

	t.Run("one display name — falls back to entity_name", func(t *testing.T) {
		st := &mockStore{
			companies: []db.CompanyLookup{appleCompany},
		}
		r := newTestResolver(t, st)

		data := mustMarshal(edgarFiling{
			DisplayNames: []string{"John Doe (CIK 0001)"},
			EntityName:   "Apple Inc",
			FormType:     "4",
		})
		cid, err := r.resolveEdgar(context.Background(), makeEvent("edgar_form4", 2, data))

		require.NoError(t, err)
		assert.Equal(t, 10, cid)
	})

	t.Run("zero display names — uses entity_name", func(t *testing.T) {
		st := &mockStore{
			companies: []db.CompanyLookup{appleCompany},
		}
		r := newTestResolver(t, st)

		data := mustMarshal(edgarFiling{
			DisplayNames: []string{},
			EntityName:   "Apple Inc",
		})
		cid, err := r.resolveEdgar(context.Background(), makeEvent("edgar_form4", 3, data))

		require.NoError(t, err)
		assert.Equal(t, 10, cid)
	})

	t.Run("company found in cache — no SearchCompanyByName call", func(t *testing.T) {
		st := &mockStore{
			companies:    []db.CompanyLookup{appleCompany},
			searchResult: nil, // should never be reached
		}
		r := newTestResolver(t, st)

		data := mustMarshal(edgarFiling{
			DisplayNames: []string{"Filer", "Apple Inc"},
		})
		cid, err := r.resolveEdgar(context.Background(), makeEvent("edgar_form4", 4, data))

		require.NoError(t, err)
		assert.Equal(t, 10, cid)
		// SearchCompanyByName should NOT have been called because the cache hit.
		// We confirm by ensuring searchResult was never consulted (no side-effects needed).
	})

	t.Run("cache miss — SearchCompanyByName fallback", func(t *testing.T) {
		st := &mockStore{
			companies:    []db.CompanyLookup{}, // empty cache
			searchResult: &db.CompanyLookup{ID: 42, Ticker: "MSFT", Name: "Microsoft Corp"},
		}
		r := newTestResolver(t, st)

		data := mustMarshal(edgarFiling{
			DisplayNames: []string{"Person", "Microsoft Corp"},
		})
		cid, err := r.resolveEdgar(context.Background(), makeEvent("edgar_form4", 5, data))

		require.NoError(t, err)
		assert.Equal(t, 42, cid)
	})

	t.Run("no company match — returns 0 with no trade insert", func(t *testing.T) {
		st := &mockStore{
			companies:  []db.CompanyLookup{},
			searchResult: nil,
			searchErr:  errors.New("not found"),
		}
		r := newTestResolver(t, st)

		data := mustMarshal(edgarFiling{
			DisplayNames: []string{"NoMatch Corp"},
		})
		cid, err := r.resolveEdgar(context.Background(), makeEvent("edgar_form4", 6, data))

		require.NoError(t, err)
		assert.Equal(t, 0, cid)
		assert.Empty(t, st.insertedTrades)
	})
}

// ---------------------------------------------------------------------------
// resolveFEC
// ---------------------------------------------------------------------------

func TestResolveFEC(t *testing.T) {
	aaplCompany := db.CompanyLookup{ID: 7, Ticker: "AAPL", Name: "Apple Inc"}

	t.Run("valid employer matching company — inserts donation", func(t *testing.T) {
		st := &mockStore{
			companies: []db.CompanyLookup{aaplCompany},
		}
		r := newTestResolver(t, st)

		data := mustMarshal(fecContribution{
			ContributorEmployer: "Apple Inc",
			ContributorName:     "Jane Smith",
			CommitteeName:       "Committee for Good Things",
			ContributionAmount:  250.00,
			ContributionDate:    "2024-11-01",
		})
		cid, err := r.resolveFEC(context.Background(), makeEvent("fec", 1, data))

		require.NoError(t, err)
		assert.Equal(t, 7, cid)
		require.Len(t, st.insertedDonations, 1)
		d := st.insertedDonations[0]
		assert.Equal(t, int64(25000), *d.AmountCents) // $250.00 → 25000 cents
		require.NotNil(t, d.DonorName)
		assert.Equal(t, "Jane Smith", *d.DonorName)
	})

	skippedEmployers := []string{
		"SELF-EMPLOYED",
		"self-employed",
		"RETIRED",
		"retired",
		"N/A",
		"n/a",
		"",
		"NONE",
		"NOT EMPLOYED",
	}
	for _, employer := range skippedEmployers {
		employer := employer
		t.Run("skipped employer: "+employer, func(t *testing.T) {
			st := &mockStore{companies: []db.CompanyLookup{aaplCompany}}
			r := newTestResolver(t, st)

			data := mustMarshal(fecContribution{ContributorEmployer: employer})
			cid, err := r.resolveFEC(context.Background(), makeEvent("fec", 2, data))

			require.NoError(t, err)
			assert.Equal(t, 0, cid)
			assert.Empty(t, st.insertedDonations)
		})
	}

	t.Run("employer not matching any company — returns 0", func(t *testing.T) {
		st := &mockStore{
			companies:  []db.CompanyLookup{},
			searchResult: nil,
			searchErr:  errors.New("not found"),
		}
		r := newTestResolver(t, st)

		data := mustMarshal(fecContribution{ContributorEmployer: "Unknown LLC"})
		cid, err := r.resolveFEC(context.Background(), makeEvent("fec", 3, data))

		require.NoError(t, err)
		assert.Equal(t, 0, cid)
	})
}

// ---------------------------------------------------------------------------
// resolveOFAC
// ---------------------------------------------------------------------------

func TestResolveOFAC(t *testing.T) {
	acmeCompany := db.CompanyLookup{ID: 55, Ticker: "ACME", Name: "Acme Corp"}

	t.Run("Entity type with matching company — inserts sanction with company link", func(t *testing.T) {
		st := &mockStore{
			companies: []db.CompanyLookup{acmeCompany},
		}
		r := newTestResolver(t, st)

		data := mustMarshal(ofacEntry{
			UID:      12345,
			LastName: "Acme Corp",
			SDNType:  "Entity",
			Programs: []string{"IRAN"},
		})
		cid, err := r.resolveOFAC(context.Background(), makeEvent("ofac_sdn", 1, data))

		require.NoError(t, err)
		assert.Equal(t, 55, cid)
		require.Len(t, st.insertedSanctions, 1)
		sn := st.insertedSanctions[0]
		require.NotNil(t, sn.CompanyID)
		assert.Equal(t, 55, *sn.CompanyID)
		require.NotNil(t, sn.Program)
		assert.Equal(t, "IRAN", *sn.Program)
	})

	t.Run("individual (empty SDNType) — inserts sanction without company link, returns 0", func(t *testing.T) {
		st := &mockStore{companies: []db.CompanyLookup{acmeCompany}}
		r := newTestResolver(t, st)

		data := mustMarshal(ofacEntry{
			UID:       9999,
			FirstName: "Ivan",
			LastName:  "Petrov",
			SDNType:   "",
			Programs:  []string{"UKRAINE-EO13685"},
		})
		cid, err := r.resolveOFAC(context.Background(), makeEvent("ofac_sdn", 2, data))

		require.NoError(t, err)
		assert.Equal(t, 0, cid)
		require.Len(t, st.insertedSanctions, 1)
		sn := st.insertedSanctions[0]
		assert.Nil(t, sn.CompanyID)
		require.NotNil(t, sn.EntityType)
		assert.Equal(t, "individual", *sn.EntityType)
	})

	t.Run("non-empty non-Entity SDNType — uses SDNType as entityType", func(t *testing.T) {
		st := &mockStore{companies: []db.CompanyLookup{}}
		r := newTestResolver(t, st)

		data := mustMarshal(ofacEntry{
			LastName: "Some Vessel",
			SDNType:  "Vessel",
		})
		cid, err := r.resolveOFAC(context.Background(), makeEvent("ofac_sdn", 3, data))

		require.NoError(t, err)
		assert.Equal(t, 0, cid)
		require.Len(t, st.insertedSanctions, 1)
		sn := st.insertedSanctions[0]
		require.NotNil(t, sn.EntityType)
		assert.Equal(t, "vessel", *sn.EntityType)
	})

	t.Run("Entity type not matching any company — inserts sanction without company link, returns 0", func(t *testing.T) {
		st := &mockStore{
			companies:  []db.CompanyLookup{},
			searchResult: nil,
			searchErr:  errors.New("not found"),
		}
		r := newTestResolver(t, st)

		data := mustMarshal(ofacEntry{
			UID:      777,
			LastName: "Unknown Entity LLC",
			SDNType:  "Entity",
		})
		cid, err := r.resolveOFAC(context.Background(), makeEvent("ofac_sdn", 4, data))

		require.NoError(t, err)
		assert.Equal(t, 0, cid)
		require.Len(t, st.insertedSanctions, 1)
		assert.Nil(t, st.insertedSanctions[0].CompanyID)
	})
}

// ---------------------------------------------------------------------------
// resolveFinnhub
// ---------------------------------------------------------------------------

func TestResolveFinnhub(t *testing.T) {
	t.Run("valid trade — inserts market_data, returns companyID", func(t *testing.T) {
		st := &mockStore{
			companies: []db.CompanyLookup{{ID: 3, Ticker: "NVDA", Name: "NVIDIA Corp"}},
		}
		r := newTestResolver(t, st)

		data := mustMarshal(finnhubTrade{Symbol: "NVDA", Price: 800.50, Volume: 250})
		cid, err := r.resolveFinnhub(context.Background(), makeEvent("finnhub", 1, data))

		require.NoError(t, err)
		assert.Equal(t, 3, cid)
		require.Len(t, st.insertedMarket, 1)
		md := st.insertedMarket[0]
		assert.Equal(t, 3, md.CompanyID)
		assert.Equal(t, "finnhub", md.Source)
		assert.Equal(t, "trade", md.DataType)
		assert.Equal(t, int64(80050), *md.PriceCents)
		assert.Equal(t, int64(250), *md.Volume)
	})

	t.Run("empty symbol returns 0", func(t *testing.T) {
		st := &mockStore{}
		r := newTestResolver(t, st)

		data := mustMarshal(finnhubTrade{Symbol: "", Price: 100.0})
		cid, err := r.resolveFinnhub(context.Background(), makeEvent("finnhub", 2, data))

		require.NoError(t, err)
		assert.Equal(t, 0, cid)
		assert.Empty(t, st.insertedMarket)
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		st := &mockStore{}
		r := newTestResolver(t, st)

		cid, err := r.resolveFinnhub(context.Background(), makeEvent("finnhub", 3, json.RawMessage(`{bad}`)))

		assert.Error(t, err)
		assert.Equal(t, 0, cid)
	})
}

// ---------------------------------------------------------------------------
// Resolve dispatch
// ---------------------------------------------------------------------------

func TestResolve_Dispatch(t *testing.T) {
	aaplCompany := db.CompanyLookup{ID: 7, Ticker: "AAPL", Name: "Apple Inc"}

	t.Run("source polygon — dispatches to resolvePolygon", func(t *testing.T) {
		st := &mockStore{companies: []db.CompanyLookup{aaplCompany}}
		r := newTestResolver(t, st)

		data := mustMarshal(polygonBar{Ticker: "AAPL", Close: 175.0, Open: 170.0, Volume: 5000})
		cid := r.Resolve(context.Background(), makeEvent("polygon", 1, data))

		assert.Equal(t, 7, cid)
		assert.Equal(t, 1, st.updateCalls)
	})

	t.Run("source federal_register — returns 0 (skipped)", func(t *testing.T) {
		st := &mockStore{}
		r := newTestResolver(t, st)

		cid := r.Resolve(context.Background(), makeEvent("federal_register", 2, mustMarshal(map[string]any{"title": "Rule"})))

		assert.Equal(t, 0, cid)
		assert.Equal(t, 0, st.updateCalls)
	})

	t.Run("source efds_senate with transactions — returns non-zero companyID", func(t *testing.T) {
		st := &mockStore{
			companies: []db.CompanyLookup{
				{ID: 7, Ticker: "AAPL", Name: "Apple Inc"},
			},
			personBySlug: map[string]db.Person{},
		}
		r := newTestResolver(t, st)

		data := mustMarshal(map[string]any{
			"first_name": "Test",
			"last_name":  "Senator",
			"transactions": []map[string]any{
				{"ticker": "AAPL", "trade_type": "Purchase", "amount_low": 1001, "amount_high": 15000},
			},
		})
		cid := r.Resolve(context.Background(), makeEvent("efds_senate", 3, data))

		assert.Equal(t, 7, cid)
		assert.Equal(t, 1, st.updateCalls)
	})

	t.Run("source efds_senate with no transactions — returns 0", func(t *testing.T) {
		st := &mockStore{}
		r := newTestResolver(t, st)

		cid := r.Resolve(context.Background(), makeEvent("efds_senate", 3, mustMarshal(map[string]any{})))

		assert.Equal(t, 0, cid)
		assert.Equal(t, 0, st.updateCalls)
	})

	t.Run("source sec_edgar_lit — dispatches to resolveSecLitigation", func(t *testing.T) {
		st := &mockStore{
			companies: []db.CompanyLookup{aaplCompany},
		}
		r := newTestResolver(t, st)
		data := mustMarshal(map[string]any{
			"id":    "LR-00001",
			"title": "SEC Charges Apple Inc for Violations",
		})
		cid := r.Resolve(context.Background(), makeEvent("sec_edgar_lit", 600, data))
		assert.Equal(t, 7, cid)
	})

	t.Run("source unknown — returns 0", func(t *testing.T) {
		st := &mockStore{}
		r := newTestResolver(t, st)

		cid := r.Resolve(context.Background(), makeEvent("made_up_source", 4, mustMarshal(map[string]any{})))

		assert.Equal(t, 0, cid)
		assert.Equal(t, 0, st.updateCalls)
	})

	t.Run("resolve error is logged, not fatal — returns 0", func(t *testing.T) {
		st := &mockStore{companies: []db.CompanyLookup{}}
		r := newTestResolver(t, st)

		// Bad JSON will cause resolvePolygon to return an error; Resolve should return 0.
		cid := r.Resolve(context.Background(), makeEvent("polygon", 5, json.RawMessage(`!!!`)))

		assert.Equal(t, 0, cid)
	})
}

// ---------------------------------------------------------------------------
// ensureCompany
// ---------------------------------------------------------------------------

func TestEnsureCompany(t *testing.T) {
	t.Run("cache hit — no upsert call", func(t *testing.T) {
		st := &mockStore{
			companies: []db.CompanyLookup{{ID: 11, Ticker: "META", Name: "Meta Platforms"}},
		}
		r := newTestResolver(t, st)

		id, err := r.ensureCompany(context.Background(), "META", "Meta Platforms")

		require.NoError(t, err)
		assert.Equal(t, 11, id)
		assert.Equal(t, 0, st.upsertCalls) // cache hit → no DB call
	})

	t.Run("cache miss — upsert called, cache updated", func(t *testing.T) {
		st := &mockStore{
			companies:    []db.CompanyLookup{},
			upsertResult: db.Company{ID: 22, Ticker: "AMZN", Name: "Amazon"},
		}
		r := newTestResolver(t, st)

		id, err := r.ensureCompany(context.Background(), "AMZN", "Amazon")

		require.NoError(t, err)
		assert.Equal(t, 22, id)
		assert.Equal(t, 1, st.upsertCalls)

		// Second call must hit cache, not DB.
		id2, err2 := r.ensureCompany(context.Background(), "AMZN", "Amazon")
		require.NoError(t, err2)
		assert.Equal(t, 22, id2)
		assert.Equal(t, 1, st.upsertCalls, "second call must use cache")
	})

	t.Run("empty ticker — returns 0 immediately", func(t *testing.T) {
		st := &mockStore{}
		r := newTestResolver(t, st)

		id, err := r.ensureCompany(context.Background(), "", "Some Name")

		require.NoError(t, err)
		assert.Equal(t, 0, id)
		assert.Equal(t, 0, st.upsertCalls)
	})

	t.Run("ticker normalized to uppercase", func(t *testing.T) {
		st := &mockStore{
			companies: []db.CompanyLookup{{ID: 33, Ticker: "GOOG", Name: "Alphabet"}},
		}
		r := newTestResolver(t, st)

		// Pass lowercase; should still find the cached uppercase entry.
		id, err := r.ensureCompany(context.Background(), "goog", "Alphabet")

		require.NoError(t, err)
		assert.Equal(t, 33, id)
		assert.Equal(t, 0, st.upsertCalls)
	})

	t.Run("concurrent calls for same new ticker — upsert called once or more, both succeed", func(t *testing.T) {
		// The resolver does not dedupe concurrent upserts; both goroutines may
		// call UpsertCompany. What matters is that both return a non-zero ID
		// and neither panics.
		st := &mockStore{
			companies:    []db.CompanyLookup{},
			upsertResult: db.Company{ID: 44, Ticker: "CONCURRENT", Name: "Concurrent Co"},
		}
		r := newTestResolver(t, st)

		const goroutines = 20
		results := make([]int, goroutines)
		var wg sync.WaitGroup
		wg.Add(goroutines)
		for i := 0; i < goroutines; i++ {
			i := i
			go func() {
				defer wg.Done()
				id, err := r.ensureCompany(context.Background(), "CONCURRENT", "Concurrent Co")
				if err == nil {
					results[i] = id
				}
			}()
		}
		wg.Wait()

		for _, id := range results {
			assert.Equal(t, 44, id)
		}
		// Upsert may have been called multiple times due to race on empty cache.
		assert.GreaterOrEqual(t, st.upsertCalls, 1)
	})
}

// ---------------------------------------------------------------------------
// Backfill
// ---------------------------------------------------------------------------

func TestBackfill(t *testing.T) {
	t.Run("two batches of events — returns correct total resolved", func(t *testing.T) {
		// Backfill breaks out of the loop when len(events) < batchSize (500).
		// To exercise two batches we must make the first batch exactly 500 items.
		aaplData := mustMarshal(polygonBar{Ticker: "AAPL", Close: 100.0, Open: 98.0, Volume: 100})
		aaplCompany := db.CompanyLookup{ID: 7, Ticker: "AAPL", Name: "Apple Inc"}

		const batchSize = 500
		batch1 := make([]db.Event, batchSize)
		for i := range batch1 {
			batch1[i] = makeEvent("polygon", i+1, aaplData)
		}
		batch2 := []db.Event{
			makeEvent("polygon", batchSize+1, aaplData),
			makeEvent("polygon", batchSize+2, aaplData),
		}

		st := &mockStore{
			companies:         []db.CompanyLookup{aaplCompany},
			unresolvedBatches: [][]db.Event{batch1, batch2},
		}
		r := newTestResolver(t, st)

		total, err := r.Backfill(context.Background(), "polygon")

		require.NoError(t, err)
		assert.Equal(t, batchSize+2, total)
	})

	t.Run("empty first batch — returns 0 immediately", func(t *testing.T) {
		st := &mockStore{
			unresolvedBatches: [][]db.Event{},
		}
		r := newTestResolver(t, st)

		total, err := r.Backfill(context.Background(), "polygon")

		require.NoError(t, err)
		assert.Equal(t, 0, total)
	})

	t.Run("ListUnresolvedEventsAfter error — returns error", func(t *testing.T) {
		st := &mockStore{
			unresolvedErr: errors.New("db connection lost"),
		}
		r := newTestResolver(t, st)

		_, err := r.Backfill(context.Background(), "polygon")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "listing unresolved events")
	})

	t.Run("context cancelled before first batch — returns context error", func(t *testing.T) {
		st := &mockStore{
			// Non-empty batches so the loop would normally continue.
			unresolvedBatches: [][]db.Event{
				{makeEvent("polygon", 1, mustMarshal(polygonBar{Ticker: "AAPL", Close: 100}))},
				{makeEvent("polygon", 2, mustMarshal(polygonBar{Ticker: "AAPL", Close: 101}))},
			},
		}
		r := newTestResolver(t, st)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately

		_, err := r.Backfill(ctx, "polygon")

		assert.ErrorIs(t, err, context.Canceled)
	})
}

// ---------------------------------------------------------------------------
// RefreshCache
// ---------------------------------------------------------------------------

func TestRefreshCache(t *testing.T) {
	t.Run("populates ticker and name maps", func(t *testing.T) {
		st := &mockStore{
			companies: []db.CompanyLookup{
				{ID: 1, Ticker: "AAPL", Name: "Apple Inc"},
				{ID: 2, Ticker: "MSFT", Name: "Microsoft Corporation"},
			},
		}
		r := newTestResolver(t, st)

		assert.Equal(t, 1, r.lookupTicker("AAPL"))
		assert.Equal(t, 2, r.lookupTicker("MSFT"))
		assert.Equal(t, 1, r.lookupName("apple inc"))
		// Normalized form should also be indexed ("microsoft" after suffix strip).
		assert.Equal(t, 2, r.lookupName("microsoft"))
	})

	t.Run("list error propagates", func(t *testing.T) {
		st := &mockStore{listErr: errors.New("db down")}
		_, err := New(context.Background(), st)
		assert.Error(t, err)
	})

	t.Run("cache is replaced on re-fresh", func(t *testing.T) {
		st := &mockStore{
			companies: []db.CompanyLookup{{ID: 1, Ticker: "OLD", Name: "Old Corp"}},
		}
		r := newTestResolver(t, st)
		assert.Equal(t, 1, r.lookupTicker("OLD"))

		// Replace the backing data and refresh.
		st.companies = []db.CompanyLookup{{ID: 2, Ticker: "NEW", Name: "New Corp"}}
		require.NoError(t, r.RefreshCache(context.Background()))

		assert.Equal(t, 0, r.lookupTicker("OLD")) // evicted
		assert.Equal(t, 2, r.lookupTicker("NEW"))  // present
	})
}

// ---------------------------------------------------------------------------
// isDuplicateError
// ---------------------------------------------------------------------------

func TestIsDuplicateError(t *testing.T) {
	assert.False(t, isDuplicateError(nil))
	assert.True(t, isDuplicateError(errors.New("duplicate key value violates unique constraint")))
	assert.True(t, isDuplicateError(errors.New("error code 23505: unique_violation")))
	assert.True(t, isDuplicateError(errors.New("UNIQUE constraint failed: events.source, events.source_id")))
	assert.False(t, isDuplicateError(errors.New("some other error")))
}

// ---------------------------------------------------------------------------
// resolveEdgar — alias hit (4th tier)
// ---------------------------------------------------------------------------

func TestResolveEdgar_AliasHit(t *testing.T) {
	st := &mockStore{
		companies:    []db.CompanyLookup{},
		searchResult: nil,
		searchErr:    errors.New("not found"),
		aliasByName: map[string]db.CompanyLookup{
			"meta platforms": {ID: 50, Ticker: "META", Name: "Meta Platforms Inc"},
		},
	}
	r := newTestResolver(t, st)

	data := mustMarshal(edgarFiling{
		DisplayNames: []string{"Person", "Meta Platforms"},
	})
	cid, err := r.resolveEdgar(context.Background(), makeEvent("edgar_form4", 10, data))

	require.NoError(t, err)
	assert.Equal(t, 50, cid)
	assert.Equal(t, 50, r.lookupName("meta platforms"))
}

// ---------------------------------------------------------------------------
// resolveEFDSTrades
// ---------------------------------------------------------------------------

func TestResolveEFDSTrades(t *testing.T) {
	t.Run("valid filing with trades — inserts congressional_trades", func(t *testing.T) {
		pelosiSlug := "nancy-pelosi"
		st := &mockStore{
			companies: []db.CompanyLookup{
				{ID: 7, Ticker: "AAPL", Name: "Apple Inc"},
				{ID: 8, Ticker: "MSFT", Name: "Microsoft Corp"},
			},
			personBySlug: map[string]db.Person{
				pelosiSlug: {ID: 1, Slug: pelosiSlug, Name: "Nancy Pelosi"},
			},
		}
		r := newTestResolver(t, st)

		data := mustMarshal(map[string]any{
			"first_name":  "Nancy",
			"last_name":   "Pelosi",
			"filing_type": "Periodic Transaction Report",
			"filing_date": "01/15/2025",
			"report_id":   "abc123",
			"transactions": []map[string]any{
				{
					"ticker":      "AAPL",
					"trade_type":  "Purchase",
					"amount_low":  1001,
					"amount_high": 15000,
					"owner":       "SP",
					"traded_at":   "2025-01-10",
				},
				{
					"ticker":      "MSFT",
					"trade_type":  "Sale (Full)",
					"amount_low":  15001,
					"amount_high": 50000,
					"owner":       "JT",
					"traded_at":   "2025-01-12",
				},
			},
		})
		cid, err := r.resolveEFDSTrades(context.Background(), makeEvent("efds_senate", 100, data))

		require.NoError(t, err)
		assert.Equal(t, 7, cid)
		require.Len(t, st.insertedCongTrades, 2)

		trade1 := st.insertedCongTrades[0]
		assert.Equal(t, 100, *trade1.EventID)
		assert.Equal(t, 1, *trade1.PersonID)
		assert.Equal(t, 7, *trade1.CompanyID)
		assert.Equal(t, "AAPL", *trade1.Ticker)
		assert.Equal(t, "Purchase", *trade1.TradeType)
		assert.Equal(t, 1001, *trade1.AmountRangeLow)
		assert.Equal(t, 15000, *trade1.AmountRangeHigh)
	})

	t.Run("no transactions field — returns 0", func(t *testing.T) {
		st := &mockStore{}
		r := newTestResolver(t, st)

		data := mustMarshal(map[string]any{
			"first_name":  "Unknown",
			"last_name":   "Senator",
			"filing_type": "Annual Report",
			"report_id":   "xyz",
		})
		cid, err := r.resolveEFDSTrades(context.Background(), makeEvent("efds_senate", 101, data))

		require.NoError(t, err)
		assert.Equal(t, 0, cid)
		assert.Empty(t, st.insertedCongTrades)
	})

	t.Run("person not found — still inserts trades without person_id", func(t *testing.T) {
		st := &mockStore{
			companies: []db.CompanyLookup{
				{ID: 7, Ticker: "AAPL", Name: "Apple Inc"},
			},
			personBySlug: map[string]db.Person{},
		}
		r := newTestResolver(t, st)

		data := mustMarshal(map[string]any{
			"first_name": "Unknown",
			"last_name":  "NewSenator",
			"transactions": []map[string]any{
				{"ticker": "AAPL", "trade_type": "Purchase", "amount_low": 1001, "amount_high": 15000},
			},
		})
		cid, err := r.resolveEFDSTrades(context.Background(), makeEvent("efds_senate", 102, data))

		require.NoError(t, err)
		assert.Equal(t, 7, cid)
		require.Len(t, st.insertedCongTrades, 1)
		assert.Nil(t, st.insertedCongTrades[0].PersonID)
	})
}

// ---------------------------------------------------------------------------
// resolveSecLitigation
// ---------------------------------------------------------------------------

func TestResolveSecLitigation(t *testing.T) {
	t.Run("release with company match — inserts court_filing", func(t *testing.T) {
		st := &mockStore{
			companies: []db.CompanyLookup{
				{ID: 55, Ticker: "ACME", Name: "Acme Corp"},
			},
		}
		r := newTestResolver(t, st)

		data := mustMarshal(map[string]any{
			"id":    "LR-25832",
			"date":  "2025-04-01",
			"title": "SEC Charges Acme Corp for Securities Fraud",
			"url":   "https://www.sec.gov/litigation/litreleases/2025/lr25832.htm",
		})
		cid, err := r.resolveSecLitigation(context.Background(), makeEvent("sec_edgar_lit", 200, data))

		require.NoError(t, err)
		assert.Equal(t, 55, cid)
		require.Len(t, st.insertedCourtFilings, 1)
		cf := st.insertedCourtFilings[0]
		assert.Equal(t, 200, *cf.EventID)
		assert.Equal(t, 55, *cf.CompanyID)
		assert.Equal(t, "LR-25832", *cf.CaseNumber)
		assert.Equal(t, "sec_litigation", *cf.FilingType)
		assert.Contains(t, cf.Parties, "Acme Corp")
	})

	t.Run("no company match — inserts filing without company_id", func(t *testing.T) {
		st := &mockStore{
			companies:    []db.CompanyLookup{},
			searchResult: nil,
			searchErr:    errors.New("not found"),
		}
		r := newTestResolver(t, st)

		data := mustMarshal(map[string]any{
			"id":    "LR-99999",
			"title": "SEC Files Action Against Unknown Person",
		})
		cid, err := r.resolveSecLitigation(context.Background(), makeEvent("sec_edgar_lit", 201, data))

		require.NoError(t, err)
		assert.Equal(t, 0, cid)
		require.Len(t, st.insertedCourtFilings, 1)
		assert.Nil(t, st.insertedCourtFilings[0].CompanyID)
	})
}

// ---------------------------------------------------------------------------
// resolveFedRegTariff
// ---------------------------------------------------------------------------

func TestResolveFedRegTariff(t *testing.T) {
	t.Run("tariff-relevant rule — inserts tariff", func(t *testing.T) {
		st := &mockStore{}
		r := newTestResolver(t, st)

		data := mustMarshal(map[string]any{
			"document_number":  "2025-00123",
			"type":             "Rule",
			"title":            "Increase in Duties on Steel Products From China",
			"publication_date": "2025-03-15",
			"effective_on":     "2025-04-01",
			"cfr_references": []map[string]any{
				{"title": 19, "part": 134},
			},
		})
		cid, err := r.resolveFedRegTariff(context.Background(), makeEvent("federal_register", 300, data))

		require.NoError(t, err)
		assert.Equal(t, 0, cid)
		require.Len(t, st.insertedTariffs, 1)
		tf := st.insertedTariffs[0]
		assert.Equal(t, 300, *tf.EventID)
		assert.Contains(t, *tf.ActionType, "Increase in Duties")
	})

	t.Run("non-tariff rule — returns 0, no insert", func(t *testing.T) {
		st := &mockStore{}
		r := newTestResolver(t, st)

		data := mustMarshal(map[string]any{
			"document_number":  "2025-00456",
			"type":             "Rule",
			"title":            "Air Quality Standards Update",
			"publication_date": "2025-03-15",
			"cfr_references": []map[string]any{
				{"title": 40, "part": 50},
			},
		})
		cid, err := r.resolveFedRegTariff(context.Background(), makeEvent("federal_register", 301, data))

		require.NoError(t, err)
		assert.Equal(t, 0, cid)
		assert.Empty(t, st.insertedTariffs)
	})

	t.Run("proposed rule with title 19 CFR — inserts tariff", func(t *testing.T) {
		st := &mockStore{}
		r := newTestResolver(t, st)

		data := mustMarshal(map[string]any{
			"document_number": "2025-00789",
			"type":            "Proposed Rule",
			"title":           "Proposed Modification of Tariff Rate Quota",
			"effective_on":    "2025-06-01",
			"cfr_references": []map[string]any{
				{"title": 19, "part": 12},
			},
		})
		cid, err := r.resolveFedRegTariff(context.Background(), makeEvent("federal_register", 302, data))

		require.NoError(t, err)
		assert.Equal(t, 0, cid)
		require.Len(t, st.insertedTariffs, 1)
	})

	t.Run("no type field — returns 0", func(t *testing.T) {
		st := &mockStore{}
		r := newTestResolver(t, st)

		data := mustMarshal(map[string]any{
			"document_number": "2025-00999",
			"title":           "Some Document",
		})
		cid, err := r.resolveFedRegTariff(context.Background(), makeEvent("federal_register", 303, data))

		require.NoError(t, err)
		assert.Equal(t, 0, cid)
		assert.Empty(t, st.insertedTariffs)
	})
}

// ---------------------------------------------------------------------------
// Compile-time check: *db.Store still satisfies ResolverStore.
// ---------------------------------------------------------------------------

var _ ResolverStore = (*db.Store)(nil)

// ---------------------------------------------------------------------------
// Backfill partial-count test (context cancelled mid-batch)
// ---------------------------------------------------------------------------

func TestBackfill_ContextCancelledMidBatch(t *testing.T) {
	// Backfill only checks ctx.Err() at the top of the loop. To exercise
	// mid-backfill cancellation the first batch must be exactly batchSize (500)
	// so the loop continues to a second iteration where ctx.Err() is checked.
	aaplCompany := db.CompanyLookup{ID: 7, Ticker: "AAPL", Name: "Apple Inc"}
	aaplData := mustMarshal(polygonBar{Ticker: "AAPL", Close: 100, Open: 98, Volume: 100})

	ctx, cancel := context.WithCancel(context.Background())

	// We need two full batches so the loop reaches a third iteration where
	// ctx.Err() is checked. The cancellingStore cancels the context on the
	// second call to ListUnresolvedEventsAfter (cancelAfter=1 means "cancel
	// once n > 1"). After both full batches are processed the loop tries a
	// third iteration, finds ctx cancelled, and returns.
	const batchSize = 500
	batch1 := make([]db.Event, batchSize)
	for i := range batch1 {
		batch1[i] = makeEvent("polygon", i+1, aaplData)
	}
	batch2 := make([]db.Event, batchSize)
	for i := range batch2 {
		batch2[i] = makeEvent("polygon", batchSize+i+1, aaplData)
	}

	var callCount atomic.Int32
	inner := &mockStore{
		companies:         []db.CompanyLookup{aaplCompany},
		unresolvedBatches: [][]db.Event{batch1, batch2},
	}

	// Cancel the context during the second ListUnresolvedEventsAfter call.
	// Batch2 is still returned and processed, but the third loop iteration
	// will see ctx.Err() != nil before fetching a (nonexistent) third batch.
	wrapped := &cancellingStore{inner: inner, cancel: cancel, cancelAfter: 1, calls: &callCount}

	r, err := New(ctx, wrapped)
	require.NoError(t, err)

	total, err := r.Backfill(ctx, "polygon")

	// Both full batches should have been resolved before the ctx check fires.
	assert.Equal(t, batchSize*2, total)
	// After the context is cancelled, Backfill must surface the cancellation.
	assert.ErrorIs(t, err, context.Canceled)
}

// cancellingStore wraps a mockStore and cancels a context after N calls to
// ListUnresolvedEventsAfter, simulating mid-backfill cancellation.
type cancellingStore struct {
	inner       *mockStore
	cancel      context.CancelFunc
	cancelAfter int32
	calls       *atomic.Int32
}

func (c *cancellingStore) ListAllCompanyLookups(ctx context.Context) ([]db.CompanyLookup, error) {
	return c.inner.ListAllCompanyLookups(ctx)
}
func (c *cancellingStore) UpsertCompany(ctx context.Context, co db.Company) (db.Company, error) {
	return c.inner.UpsertCompany(ctx, co)
}
func (c *cancellingStore) EnsureCompany(ctx context.Context, co db.Company) (db.Company, error) {
	return c.inner.EnsureCompany(ctx, co)
}
func (c *cancellingStore) UpdateEventCompanyID(ctx context.Context, eventID, companyID int) error {
	return c.inner.UpdateEventCompanyID(ctx, eventID, companyID)
}
func (c *cancellingStore) SearchCompanyByName(ctx context.Context, name string) (*db.CompanyLookup, error) {
	return c.inner.SearchCompanyByName(ctx, name)
}
func (c *cancellingStore) InsertMarketData(ctx context.Context, m db.MarketDataRow) error {
	return c.inner.InsertMarketData(ctx, m)
}
func (c *cancellingStore) InsertInsiderTrade(ctx context.Context, t db.InsiderTrade) error {
	return c.inner.InsertInsiderTrade(ctx, t)
}
func (c *cancellingStore) InsertDonation(ctx context.Context, d db.Donation) error {
	return c.inner.InsertDonation(ctx, d)
}
func (c *cancellingStore) InsertContract(ctx context.Context, co db.Contract) error {
	return c.inner.InsertContract(ctx, co)
}
func (c *cancellingStore) InsertSanction(ctx context.Context, s db.Sanction) error {
	return c.inner.InsertSanction(ctx, s)
}
func (c *cancellingStore) InsertWarnFiling(ctx context.Context, w db.WarnFiling) error {
	return c.inner.InsertWarnFiling(ctx, w)
}
func (c *cancellingStore) InsertCongressionalTrade(ctx context.Context, t db.CongressionalTrade) error {
	return c.inner.InsertCongressionalTrade(ctx, t)
}
func (c *cancellingStore) InsertCourtFiling(ctx context.Context, cf db.CourtFiling) error {
	return c.inner.InsertCourtFiling(ctx, cf)
}
func (c *cancellingStore) InsertTariff(ctx context.Context, t db.Tariff) error {
	return c.inner.InsertTariff(ctx, t)
}
func (c *cancellingStore) GetCompanyByAlias(ctx context.Context, alias string) (db.CompanyLookup, error) {
	return c.inner.GetCompanyByAlias(ctx, alias)
}
func (c *cancellingStore) InsertEntityAlias(ctx context.Context, a db.EntityAlias) (db.EntityAlias, error) {
	return c.inner.InsertEntityAlias(ctx, a)
}
func (c *cancellingStore) GetPersonBySlug(ctx context.Context, slug string) (db.Person, error) {
	return c.inner.GetPersonBySlug(ctx, slug)
}
func (c *cancellingStore) UpsertPerson(ctx context.Context, p db.Person) (db.Person, error) {
	return c.inner.UpsertPerson(ctx, p)
}
func (c *cancellingStore) ListUnresolvedEventsAfter(ctx context.Context, source string, afterID, batchSize int) ([]db.Event, error) {
	n := c.calls.Add(1)
	if n > c.cancelAfter {
		c.cancel()
	}
	return c.inner.ListUnresolvedEventsAfter(ctx, source, afterID, batchSize)
}
