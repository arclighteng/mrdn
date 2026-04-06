package score

import (
	"context"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// mockScoreStore
// ---------------------------------------------------------------------------

type mockScoreStore struct {
	marketData    []db.MarketDataRow
	insiderTrades []db.InsiderTrade
	sanctions     []db.Sanction
	contracts     []db.Contract
	donations     []db.Donation
	insertedScore *db.Score
}

func (m *mockScoreStore) GetMarketDataRange(_ context.Context, _ int, _, _ time.Time) ([]db.MarketDataRow, error) {
	return m.marketData, nil
}
func (m *mockScoreStore) GetInsiderTradesRange(_ context.Context, _ int, _, _ time.Time) ([]db.InsiderTrade, error) {
	return m.insiderTrades, nil
}
func (m *mockScoreStore) GetSanctionsRange(_ context.Context, _ int, _, _ time.Time) ([]db.Sanction, error) {
	return m.sanctions, nil
}
func (m *mockScoreStore) GetContractsRange(_ context.Context, _ int, _, _ time.Time) ([]db.Contract, error) {
	return m.contracts, nil
}
func (m *mockScoreStore) GetDonationsRange(_ context.Context, _ int, _, _ time.Time) ([]db.Donation, error) {
	return m.donations, nil
}
func (m *mockScoreStore) InsertScore(_ context.Context, sc db.Score) error {
	m.insertedScore = &sc
	return nil
}

// ---------------------------------------------------------------------------
// mockSubScorer — returns a fixed value
// ---------------------------------------------------------------------------

type mockSubScorer struct{ val float64 }

func (ms *mockSubScorer) Score(_ context.Context, _ int, _ time.Time) (float64, error) {
	return ms.val, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func ptrInt64(v int64) *int64 { return &v }
func ptrTime(t time.Time) *time.Time { return &t }

var testNow = time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)

// ---------------------------------------------------------------------------
// Engine composite tests
// ---------------------------------------------------------------------------

func TestComposite(t *testing.T) {
	// weights: market=0.35, policy=0.40, insider=0.25
	// sub-scores: market=80, policy=60, insider=70
	// composite = 0.35*80 + 0.40*60 + 0.25*70 = 28 + 24 + 17.5 = 69.5
	store := &mockScoreStore{}
	engine := NewEngine(
		store,
		&mockSubScorer{80},
		&mockSubScorer{60},
		&mockSubScorer{70},
		DefaultWeights(),
	)

	sc, err := engine.Compute(context.Background(), 1, testNow)
	require.NoError(t, err)
	assert.InDelta(t, 69.5, sc.CompositeScore, 0.01)
	assert.InDelta(t, 80.0, sc.MarketScore, 0.01)
	assert.InDelta(t, 60.0, sc.PolicyScore, 0.01)
	assert.InDelta(t, 70.0, sc.InsiderScore, 0.01)
	assert.Equal(t, 1, sc.CompanyID)
	assert.Equal(t, testNow, sc.ComputedAt)
}

func TestComposite_AllNeutral(t *testing.T) {
	// When all sub-scores are 50, composite must be exactly 50.
	store := &mockScoreStore{}
	engine := NewEngine(
		store,
		&mockSubScorer{50},
		&mockSubScorer{50},
		&mockSubScorer{50},
		DefaultWeights(),
	)

	sc, err := engine.Compute(context.Background(), 42, testNow)
	require.NoError(t, err)
	assert.InDelta(t, 50.0, sc.CompositeScore, 0.01)
}

func TestComposite_Clamped(t *testing.T) {
	// Sub-scores of 100 with weights summing to 1.0 → 100 exactly (no clamping needed),
	// but using weights > 1 or scores > 100 the composite would exceed 100.
	// We verify clamp by injecting sub-scores of 100 and custom weights > 1.
	store := &mockScoreStore{}
	engine := NewEngine(
		store,
		&mockSubScorer{100},
		&mockSubScorer{100},
		&mockSubScorer{100},
		Weights{Market: 0.5, Policy: 0.5, Insider: 0.5}, // sums to 1.5
	)

	sc, err := engine.Compute(context.Background(), 1, testNow)
	require.NoError(t, err)
	assert.Equal(t, 100.0, sc.CompositeScore, "composite must be clamped to 100")
}

// ---------------------------------------------------------------------------
// Engine integration test with mock store
// ---------------------------------------------------------------------------

func TestCompute_Integration(t *testing.T) {
	// Provide concrete data through the mock store and use real sub-scorers.
	// Market: 5 ascending prices, no volume anomaly, 0 insider trades
	//   priceTrend: 100→50 mapped (100% gain) → clamped 100; volumeAnomaly: 50 (no volume data)
	//   insiderActivity: 50 (0 trades)
	//   market = 0.30*100 + 0.30*50 + 0.40*50 = 30 + 15 + 20 = 65
	//
	// Policy: 2 sanctions, no contracts, no donations
	//   sanctions: 70, contracts: 50, donations: 50
	//   policy = 0.40*70 + 0.35*50 + 0.25*50 = 28 + 17.5 + 12.5 = 58
	//
	// Insider: 0 trades, 0 donations → 50
	//   insider = 0.50*50 + 0.50*50 = 50
	//
	// composite = 0.35*65 + 0.40*58 + 0.25*50 = 22.75 + 23.2 + 12.5 = 58.45

	prices := []int64{100_00, 120_00, 140_00, 160_00, 200_00} // cents, 100% gain
	mktRows := make([]db.MarketDataRow, len(prices))
	for i, p := range prices {
		p := p // capture
		mktRows[i] = db.MarketDataRow{
			ID:         i + 1,
			CompanyID:  1,
			Source:     "test",
			DataType:   "quote",
			PriceCents: &p,
			RecordedAt: testNow.Add(-time.Duration(len(prices)-i) * time.Hour),
		}
	}

	addedAt := testNow.Add(-24 * time.Hour)
	store := &mockScoreStore{
		marketData: mktRows,
		sanctions: []db.Sanction{
			{ID: 1, AddedAt: &addedAt},
			{ID: 2, AddedAt: &addedAt},
		},
	}

	engine := NewEngine(
		store,
		NewMarketScorer(store),
		NewPolicyScorer(store),
		NewInsiderScorer(store),
		DefaultWeights(),
	)

	sc, err := engine.Compute(context.Background(), 1, testNow)
	require.NoError(t, err)

	assert.InDelta(t, 65.0, sc.MarketScore, 0.01, "market score")
	assert.InDelta(t, 58.0, sc.PolicyScore, 0.01, "policy score")
	assert.InDelta(t, 50.0, sc.InsiderScore, 0.01, "insider score")
	assert.InDelta(t, 58.45, sc.CompositeScore, 0.01, "composite score")
}

// ---------------------------------------------------------------------------
// MarketScorer tests
// ---------------------------------------------------------------------------

func TestMarketScorer_NoData(t *testing.T) {
	store := &mockScoreStore{}
	ms := NewMarketScorer(store)
	score, err := ms.Score(context.Background(), 1, testNow)
	require.NoError(t, err)
	assert.Equal(t, 50.0, score)
}

func TestMarketScorer_StrongUptrend(t *testing.T) {
	// 5 strictly ascending prices: clear uptrend → score > 65.
	prices := []int64{100_00, 105_00, 110_00, 115_00, 120_00} // +20% change
	rows := makePriceRows(prices)
	store := &mockScoreStore{marketData: rows}
	ms := NewMarketScorer(store)

	score, err := ms.Score(context.Background(), 1, testNow)
	require.NoError(t, err)
	// +20% price change → priceTrend=100, no volume data → 50, no insider trades → 50
	// market = 0.30*100 + 0.30*50 + 0.40*50 = 30+15+20 = 65
	assert.InDelta(t, 65.0, score, 0.01, "strong uptrend should score at 65")
	assert.GreaterOrEqual(t, score, 65.0)
}

func TestMarketScorer_Downtrend(t *testing.T) {
	// 5 strictly descending prices: downtrend → score < 50.
	prices := []int64{120_00, 115_00, 110_00, 105_00, 100_00} // −16.7% change
	rows := makePriceRows(prices)
	store := &mockScoreStore{marketData: rows}
	ms := NewMarketScorer(store)

	score, err := ms.Score(context.Background(), 1, testNow)
	require.NoError(t, err)
	assert.Less(t, score, 50.0, "downtrend should score below 50")
}

func TestMarketScorer_HighVolume(t *testing.T) {
	// Latest volume is exactly 2x the average → volumeAnomaly component near 100.
	// We provide 3 rows: avg volume = (50+50+100)/3 = 66.67, latest = 100.
	// ratio = 100/66.67 ≈ 1.5 → score = (1.5-0.5)/(2.0-0.5)*100 = 66.67
	// No prices → priceTrend = 50, insiderActivity = 50.
	// market = 0.30*50 + 0.30*66.67 + 0.40*50 = 15 + 20 + 20 = 55
	v1, v2, v3 := int64(50), int64(50), int64(100)
	rows := []db.MarketDataRow{
		{ID: 1, CompanyID: 1, Source: "t", DataType: "quote", Volume: &v1, RecordedAt: testNow.Add(-2 * time.Hour)},
		{ID: 2, CompanyID: 1, Source: "t", DataType: "quote", Volume: &v2, RecordedAt: testNow.Add(-1 * time.Hour)},
		{ID: 3, CompanyID: 1, Source: "t", DataType: "quote", Volume: &v3, RecordedAt: testNow},
	}
	store := &mockScoreStore{marketData: rows}
	ms := NewMarketScorer(store)

	score, err := ms.Score(context.Background(), 1, testNow)
	require.NoError(t, err)
	// Volume component = 66.67; overall market score should be elevated above 50.
	assert.Greater(t, score, 50.0, "high volume should push score above 50")
}

func TestMarketScorer_ExactCalculation(t *testing.T) {
	// Controlled inputs: 5 prices with +5% change, no volume data, 2 insider trades.
	// priceTrend: changePct = 0.05 → score = (0.05-(-0.10))/(0.10-(-0.10))*100 = 0.15/0.20*100 = 75
	// volumeAnomaly: no volume → 50
	// insiderActivity: 2 trades → 60
	// market = 0.30*75 + 0.30*50 + 0.40*60 = 22.5 + 15 + 24 = 61.5
	prices := []int64{100_00, 101_00, 102_00, 103_00, 105_00} // exactly +5%
	rows := makePriceRows(prices)
	store := &mockScoreStore{
		marketData:    rows,
		insiderTrades: makeInsiderTrades(2),
	}
	ms := NewMarketScorer(store)

	score, err := ms.Score(context.Background(), 1, testNow)
	require.NoError(t, err)
	assert.InDelta(t, 61.5, score, 0.01)
}

// ---------------------------------------------------------------------------
// PolicyScorer tests
// ---------------------------------------------------------------------------

func TestPolicyScorer_NoData(t *testing.T) {
	store := &mockScoreStore{}
	ps := NewPolicyScorer(store)
	score, err := ps.Score(context.Background(), 1, testNow)
	require.NoError(t, err)
	assert.Equal(t, 50.0, score)
}

func TestPolicyScorer_WithSanctions(t *testing.T) {
	// 3 sanctions → sanctionsScore=85; no contracts or donations → 50 each.
	// policy = 0.40*85 + 0.35*50 + 0.25*50 = 34 + 17.5 + 12.5 = 64
	addedAt := testNow.Add(-time.Hour)
	store := &mockScoreStore{
		sanctions: []db.Sanction{
			{ID: 1, AddedAt: &addedAt},
			{ID: 2, AddedAt: &addedAt},
			{ID: 3, AddedAt: &addedAt},
		},
	}
	ps := NewPolicyScorer(store)
	score, err := ps.Score(context.Background(), 1, testNow)
	require.NoError(t, err)
	// sanctionsScore=85, contractsScore=50, donationsScore=50
	// policy = 0.40*85 + 0.35*50 + 0.25*50 = 34+17.5+12.5 = 64
	assert.InDelta(t, 64.0, score, 0.01, "3 sanctions should produce a policy score of 64")
	assert.Greater(t, score, 60.0, "3 sanctions should push policy score meaningfully above neutral")
}

func TestPolicyScorer_WithContracts(t *testing.T) {
	// $50M contract → ratio=0.5 → contractsScore = 50 + 0.5*25 = 62.5
	// no sanctions, no donations → 50 each
	// policy = 0.40*50 + 0.35*62.5 + 0.25*50 = 20 + 21.875 + 12.5 = 54.375
	awardedAt := testNow.Add(-time.Hour)
	amountCents := int64(50_000_000 * 100) // $50M in cents
	store := &mockScoreStore{
		contracts: []db.Contract{
			{ID: 1, AwardedAt: &awardedAt, AmountCents: &amountCents},
		},
	}
	ps := NewPolicyScorer(store)
	score, err := ps.Score(context.Background(), 1, testNow)
	require.NoError(t, err)
	assert.Greater(t, score, 50.0, "$50M contract should push score above 50")
	assert.InDelta(t, 54.375, score, 0.01)
}

// ---------------------------------------------------------------------------
// InsiderScorer tests
// ---------------------------------------------------------------------------

func TestInsiderScorer_NoData(t *testing.T) {
	store := &mockScoreStore{}
	is := NewInsiderScorer(store)
	score, err := is.Score(context.Background(), 1, testNow)
	require.NoError(t, err)
	assert.Equal(t, 50.0, score)
}

func TestInsiderScorer_WithTrades(t *testing.T) {
	// 5 trades → insiderTradesScore=80; no donations → donationsScore=50.
	// insider = 0.50*80 + 0.50*50 = 40 + 25 = 65
	store := &mockScoreStore{insiderTrades: makeInsiderTrades(5)}
	is := NewInsiderScorer(store)
	score, err := is.Score(context.Background(), 1, testNow)
	require.NoError(t, err)
	// insiderTradesScore(5)=80, insiderDonationsScore([])=50
	// insider = 0.50*80 + 0.50*50 = 40+25 = 65
	assert.InDelta(t, 65.0, score, 0.01, "5 insider trades with no donations should score 65")
	assert.GreaterOrEqual(t, score, 65.0)
}

func TestInsiderScorer_ExactCalculation(t *testing.T) {
	// 2 insider trades → insiderTradesScore=65; $50K donation → donationsScore=75.
	// insider = 0.50*65 + 0.50*75 = 32.5 + 37.5 = 70
	donatedAt := testNow.Add(-time.Hour)
	amountCents := int64(50_000 * 100) // $50K in cents
	store := &mockScoreStore{
		insiderTrades: makeInsiderTrades(2),
		donations: []db.Donation{
			{ID: 1, DonatedAt: &donatedAt, AmountCents: &amountCents},
		},
	}
	is := NewInsiderScorer(store)
	score, err := is.Score(context.Background(), 1, testNow)
	require.NoError(t, err)
	assert.InDelta(t, 70.0, score, 0.01)
}

// ---------------------------------------------------------------------------
// Helper constructors
// ---------------------------------------------------------------------------

// makePriceRows builds MarketDataRow slices with ascending timestamps.
func makePriceRows(prices []int64) []db.MarketDataRow {
	rows := make([]db.MarketDataRow, len(prices))
	for i, p := range prices {
		p := p
		rows[i] = db.MarketDataRow{
			ID:         i + 1,
			CompanyID:  1,
			Source:     "test",
			DataType:   "quote",
			PriceCents: &p,
			RecordedAt: testNow.Add(-time.Duration(len(prices)-i) * time.Hour),
		}
	}
	return rows
}

// makeInsiderTrades builds n InsiderTrade stubs with filed_at set.
func makeInsiderTrades(n int) []db.InsiderTrade {
	trades := make([]db.InsiderTrade, n)
	for i := range trades {
		filedAt := testNow.Add(-time.Duration(i+1) * time.Hour)
		trades[i] = db.InsiderTrade{
			ID:      i + 1,
			FiledAt: &filedAt,
		}
	}
	return trades
}
