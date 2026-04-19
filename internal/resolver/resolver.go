package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

// ResolverStore is the subset of db.Store methods that the Resolver needs.
// Defined as an interface so tests can supply a mock without a real database.
type ResolverStore interface {
	ListAllCompanyLookups(ctx context.Context) ([]db.CompanyLookup, error)
	UpsertCompany(ctx context.Context, c db.Company) (db.Company, error)
	EnsureCompany(ctx context.Context, c db.Company) (db.Company, error)
	UpdateEventCompanyID(ctx context.Context, eventID int, companyID int) error
	SearchCompanyByName(ctx context.Context, name string) (*db.CompanyLookup, error)
	InsertMarketData(ctx context.Context, m db.MarketDataRow) error
	InsertInsiderTrade(ctx context.Context, t db.InsiderTrade) error
	InsertDonation(ctx context.Context, d db.Donation) error
	InsertContract(ctx context.Context, c db.Contract) error
	InsertSanction(ctx context.Context, sn db.Sanction) error
	InsertWarnFiling(ctx context.Context, w db.WarnFiling) error
	ListUnresolvedEventsAfter(ctx context.Context, source string, afterID, batchSize int) ([]db.Event, error)
}

// Resolver matches events to companies and extracts typed records into domain
// tables. It maintains an in-memory ticker→companyID cache that is refreshed
// periodically.
type Resolver struct {
	store ResolverStore

	mu          sync.RWMutex
	byTicker    map[string]int // ticker → company ID
	byNameLower map[string]int // lowercase name → company ID
}

// New creates a Resolver and loads the initial company cache.
func New(ctx context.Context, store ResolverStore) (*Resolver, error) {
	r := &Resolver{store: store}
	if err := r.RefreshCache(ctx); err != nil {
		return nil, fmt.Errorf("resolver: initial cache load: %w", err)
	}
	return r, nil
}

// RefreshCache reloads all companies from the database into the in-memory maps.
func (r *Resolver) RefreshCache(ctx context.Context) error {
	companies, err := r.store.ListAllCompanyLookups(ctx)
	if err != nil {
		return err
	}

	byTicker := make(map[string]int, len(companies))
	byNameLower := make(map[string]int, len(companies))
	for _, c := range companies {
		byTicker[strings.ToUpper(c.Ticker)] = c.ID
		lower := strings.ToLower(c.Name)
		byNameLower[lower] = c.ID
		// Also index the normalized (suffix-stripped) form.
		if norm := normalizeName(lower); norm != lower {
			byNameLower[norm] = c.ID
		}
	}

	r.mu.Lock()
	r.byTicker = byTicker
	r.byNameLower = byNameLower
	r.mu.Unlock()
	return nil
}

// lookupTicker returns the company ID for a ticker, or 0 if not found.
func (r *Resolver) lookupTicker(ticker string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byTicker[strings.ToUpper(ticker)]
}

// lookupName returns the company ID for a name match (case-insensitive).
// Tries exact match first, then normalized (suffix-stripped) form.
func (r *Resolver) lookupName(name string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	lower := strings.ToLower(strings.TrimSpace(name))
	if id := r.byNameLower[lower]; id != 0 {
		return id
	}
	return r.byNameLower[normalizeName(lower)]
}

// ensureCompany looks up a ticker in cache; if missing, upserts the company
// into the database and adds it to the cache. Returns the company ID.
func (r *Resolver) ensureCompany(ctx context.Context, ticker, name string) (int, error) {
	ticker = strings.ToUpper(strings.TrimSpace(ticker))
	if ticker == "" {
		return 0, nil
	}

	if id := r.lookupTicker(ticker); id != 0 {
		return id, nil
	}

	// Not in cache — insert if not exists. Never overwrite an existing row:
	// the resolver's name is a ticker fallback, not an authoritative source.
	if name == "" {
		name = ticker
	}
	company, err := r.store.EnsureCompany(ctx, db.Company{
		Ticker: ticker,
		Name:   name,
	})
	if err != nil {
		return 0, fmt.Errorf("resolver: upserting company %s: %w", ticker, err)
	}

	// Update cache.
	r.mu.Lock()
	r.byTicker[strings.ToUpper(company.Ticker)] = company.ID
	r.byNameLower[strings.ToLower(company.Name)] = company.ID
	r.mu.Unlock()

	return company.ID, nil
}

// Resolve processes a single event: links it to a company (if possible) and
// extracts typed records into domain tables. Returns the matched company ID
// (0 if unresolved). Errors are logged but not fatal — a failed resolve should
// not block ingestion.
func (r *Resolver) Resolve(ctx context.Context, evt db.Event) int {
	var companyID int
	var err error

	switch evt.Source {
	case "polygon":
		companyID, err = r.resolvePolygon(ctx, evt)
	case "edgar_form4":
		companyID, err = r.resolveEdgar(ctx, evt)
	case "fec":
		companyID, err = r.resolveFEC(ctx, evt)
	case "usaspending":
		companyID, err = r.resolveUSASpending(ctx, evt)
	case "ofac_sdn":
		companyID, err = r.resolveOFAC(ctx, evt)
	case "federal_register":
		// Federal Register events are regulatory actions — hard to match to a
		// single company without NLP. Skip entity resolution for now.
		return 0
	case "efds_senate":
		// Senate financial disclosures need person-level resolution, not company.
		return 0
	case "finnhub":
		companyID, err = r.resolveFinnhub(ctx, evt)
	case "warn":
		companyID, err = r.resolveWarn(ctx, evt)
	default:
		return 0
	}

	if err != nil {
		log.Printf("[resolver] %s event %d: %v", evt.Source, evt.ID, err)
		return 0
	}

	if companyID > 0 {
		if uerr := r.store.UpdateEventCompanyID(ctx, evt.ID, companyID); uerr != nil {
			log.Printf("[resolver] failed to update event %d company_id: %v", evt.ID, uerr)
		}
	}

	return companyID
}

// --- Source-specific resolvers ---

// polygonBar mirrors the JSON shape stored by the Polygon parser.
type polygonBar struct {
	Ticker    string  `json:"T"`
	Open      float64 `json:"o"`
	High      float64 `json:"h"`
	Low       float64 `json:"l"`
	Close     float64 `json:"c"`
	Volume    float64 `json:"v"`
	Timestamp int64   `json:"t"`
}

func (r *Resolver) resolvePolygon(ctx context.Context, evt db.Event) (int, error) {
	var bar polygonBar
	if err := json.Unmarshal(evt.EventData, &bar); err != nil {
		return 0, fmt.Errorf("unmarshal polygon bar: %w", err)
	}
	if bar.Ticker == "" {
		return 0, nil
	}

	companyID, err := r.ensureCompany(ctx, bar.Ticker, "")
	if err != nil {
		return 0, err
	}
	if companyID == 0 {
		return 0, nil
	}

	// Extract typed market_data record.
	priceCents := int64(math.Round(bar.Close * 100))
	volume := int64(bar.Volume)
	var changePct *float64
	if bar.Open > 0 {
		pct := ((bar.Close - bar.Open) / bar.Open) * 100
		changePct = &pct
	}

	if err := r.store.InsertMarketData(ctx, db.MarketDataRow{
		CompanyID:  companyID,
		Source:     "polygon",
		DataType:   "daily",
		PriceCents: &priceCents,
		Volume:     &volume,
		ChangePct:  changePct,
		RecordedAt: evt.OccurredAt,
	}); err != nil {
		log.Printf("[resolver] polygon market_data insert for %s: %v", bar.Ticker, err)
	}

	return companyID, nil
}

// edgarFiling mirrors the JSON stored by the EDGAR parser.
type edgarFiling struct {
	FileNum        json.RawMessage `json:"file_num"`
	DisplayNames   []string        `json:"display_names"`
	FormType       string          `json:"form_type"`
	Form           string          `json:"form"`
	FileDate       string          `json:"file_date"`
	PeriodOfReport string          `json:"period_of_report"`
	PeriodEnding   string          `json:"period_ending"`
	EntityName     string          `json:"entity_name"`
	ADSH           string          `json:"adsh"`
}

func (r *Resolver) resolveEdgar(ctx context.Context, evt db.Event) (int, error) {
	var filing edgarFiling
	if err := json.Unmarshal(evt.EventData, &filing); err != nil {
		return 0, fmt.Errorf("unmarshal edgar filing: %w", err)
	}

	// Try to find a company from display_names. The second entry is typically
	// the company (first is the filer/person).
	var companyName string
	var filerName string
	for i, dn := range filing.DisplayNames {
		// Strip CIK suffix: "Apple Inc. (CIK 0000320193)" → "Apple Inc."
		name := stripCIK(dn)
		if i == 0 {
			filerName = name
		} else if i == 1 {
			companyName = name
		}
	}

	if companyName == "" && filing.EntityName != "" {
		companyName = filing.EntityName
	}

	var companyID int
	if companyName != "" {
		companyID = r.lookupName(companyName)
		if companyID == 0 {
			// Try DB search as fallback.
			c, err := r.store.SearchCompanyByName(ctx, companyName)
			if err == nil && c != nil {
				companyID = c.ID
				// Update cache.
				r.mu.Lock()
				r.byNameLower[strings.ToLower(companyName)] = companyID
				r.mu.Unlock()
			}
		}
	}

	if companyID == 0 {
		log.Printf("[resolver] edgar event %d: no company match for %q", evt.ID, companyName)
		return 0, nil
	}

	// Extract insider_trade typed record.
	var filedAt *time.Time
	dateStr := filing.FileDate
	if dateStr == "" {
		dateStr = filing.PeriodOfReport
	}
	if dateStr == "" {
		dateStr = filing.PeriodEnding
	}
	if dateStr != "" {
		if t, err := time.Parse("2006-01-02", dateStr); err == nil {
			ft := t.UTC()
			filedAt = &ft
		}
	}

	if err := r.store.InsertInsiderTrade(ctx, db.InsiderTrade{
		EventID:   &evt.ID,
		CompanyID: &companyID,
		FilerName: strPtr(filerName),
		TradeType: strPtr("form4"),
		FiledAt:   filedAt,
	}); err != nil {
		log.Printf("[resolver] edgar insider_trade insert: %v", err)
	}

	return companyID, nil
}

// stripCIK removes the "(CIK ...)" suffix from EDGAR display names.
func stripCIK(s string) string {
	if idx := strings.Index(s, "(CIK"); idx > 0 {
		return strings.TrimSpace(s[:idx])
	}
	return strings.TrimSpace(s)
}

// fecContribution mirrors the JSON stored by the FEC parser.
type fecContribution struct {
	ContributorEmployer string  `json:"contributor_employer"`
	ContributorName     string  `json:"contributor_name"`
	CommitteeName       string  `json:"committee_name"`
	ContributionAmount  float64 `json:"contribution_receipt_amount"`
	ContributionDate    string  `json:"contribution_receipt_date"`
}

func (r *Resolver) resolveFEC(ctx context.Context, evt db.Event) (int, error) {
	var contrib fecContribution
	if err := json.Unmarshal(evt.EventData, &contrib); err != nil {
		return 0, fmt.Errorf("unmarshal fec contribution: %w", err)
	}

	employer := strings.TrimSpace(contrib.ContributorEmployer)
	if employer == "" || strings.EqualFold(employer, "SELF-EMPLOYED") ||
		strings.EqualFold(employer, "RETIRED") || strings.EqualFold(employer, "N/A") ||
		strings.EqualFold(employer, "NONE") || strings.EqualFold(employer, "NOT EMPLOYED") {
		return 0, nil
	}

	companyID := r.lookupName(employer)
	if companyID == 0 {
		c, err := r.store.SearchCompanyByName(ctx, employer)
		if err == nil && c != nil {
			companyID = c.ID
			r.mu.Lock()
			r.byNameLower[strings.ToLower(employer)] = companyID
			r.mu.Unlock()
		}
	}

	if companyID == 0 {
		return 0, nil
	}

	// Extract donation typed record.
	amountCents := int64(math.Round(contrib.ContributionAmount * 100))
	var donatedAt *time.Time
	if contrib.ContributionDate != "" {
		if t, err := time.Parse("2006-01-02", contrib.ContributionDate); err == nil {
			dt := t.UTC()
			donatedAt = &dt
		}
	}

	if err := r.store.InsertDonation(ctx, db.Donation{
		EventID:       &evt.ID,
		CompanyID:     &companyID,
		DonorName:     strPtr(contrib.ContributorName),
		DonorType:     strPtr("individual"),
		DonorEmployer: strPtr(employer),
		Recipient:     strPtr(contrib.CommitteeName),
		AmountCents:   &amountCents,
		DonatedAt:     donatedAt,
	}); err != nil {
		log.Printf("[resolver] fec donation insert: %v", err)
	}

	return companyID, nil
}

// usaspendingAward mirrors the JSON stored by the USAspending parser.
type usaspendingAward struct {
	RecipientName       string  `json:"Recipient Name"`
	AwardAmount         float64 `json:"Award Amount"`
	AwardType           string  `json:"Award Type"`
	AwardingAgency      string  `json:"Awarding Agency"`
	AwardID             string  `json:"Award ID"`
	StartDate           string  `json:"Start Date"`
	GeneratedInternalID string  `json:"generated_internal_id"`
}

func (r *Resolver) resolveUSASpending(ctx context.Context, evt db.Event) (int, error) {
	var award usaspendingAward
	if err := json.Unmarshal(evt.EventData, &award); err != nil {
		return 0, fmt.Errorf("unmarshal usaspending award: %w", err)
	}

	recipient := strings.TrimSpace(award.RecipientName)
	if recipient == "" {
		return 0, nil
	}

	companyID := r.lookupName(recipient)
	if companyID == 0 {
		c, err := r.store.SearchCompanyByName(ctx, recipient)
		if err == nil && c != nil {
			companyID = c.ID
			r.mu.Lock()
			r.byNameLower[strings.ToLower(recipient)] = companyID
			r.mu.Unlock()
		}
	}

	if companyID == 0 {
		return 0, nil
	}

	// Extract contract typed record.
	amountCents := int64(math.Round(award.AwardAmount * 100))
	var awardedAt *time.Time
	if award.StartDate != "" {
		if t, err := time.Parse("2006-01-02", award.StartDate); err == nil {
			at := t.UTC()
			awardedAt = &at
		}
	}

	if err := r.store.InsertContract(ctx, db.Contract{
		EventID:     &evt.ID,
		CompanyID:   &companyID,
		Agency:      strPtr(award.AwardingAgency),
		AmountCents: &amountCents,
		ActionType:  strPtr(award.AwardType),
		Description: strPtr(award.AwardID),
		AwardedAt:   awardedAt,
	}); err != nil {
		log.Printf("[resolver] usaspending contract insert: %v", err)
	}

	return companyID, nil
}

// ofacEntry mirrors the JSON stored by the OFAC parser (camelCase keys).
type ofacEntry struct {
	UID       int      `json:"uid"`
	FirstName string   `json:"firstName"`
	LastName  string   `json:"lastName"`
	SDNType   string   `json:"sdnType"`
	Programs  []string `json:"programs"`
}

func (r *Resolver) resolveOFAC(ctx context.Context, evt db.Event) (int, error) {
	var entry ofacEntry
	if err := json.Unmarshal(evt.EventData, &entry); err != nil {
		return 0, fmt.Errorf("unmarshal ofac entry: %w", err)
	}

	// Only try to match entities (companies), not individuals.
	if entry.SDNType != "Entity" {
		// Still insert sanction record without company link.
		name := strings.TrimSpace(entry.LastName)
		if entry.FirstName != "" {
			name = strings.TrimSpace(entry.FirstName) + " " + name
		}
		entityType := "individual"
		if entry.SDNType != "" {
			entityType = strings.ToLower(entry.SDNType)
		}
		var program *string
		if len(entry.Programs) > 0 {
			program = &entry.Programs[0]
		}
		if err := r.store.InsertSanction(ctx, db.Sanction{
			EventID:    &evt.ID,
			EntityName: strPtr(name),
			EntityType: strPtr(entityType),
			Program:    program,
			AddedAt:    &evt.OccurredAt,
		}); err != nil {
			log.Printf("[resolver] ofac sanction insert (individual): %v", err)
		}
		return 0, nil
	}

	entityName := strings.TrimSpace(entry.LastName)
	companyID := r.lookupName(entityName)
	if companyID == 0 {
		c, err := r.store.SearchCompanyByName(ctx, entityName)
		if err == nil && c != nil {
			companyID = c.ID
		}
	}

	var companyIDPtr *int
	if companyID > 0 {
		companyIDPtr = &companyID
	}

	var program *string
	if len(entry.Programs) > 0 {
		program = &entry.Programs[0]
	}

	if err := r.store.InsertSanction(ctx, db.Sanction{
		EventID:    &evt.ID,
		CompanyID:  companyIDPtr,
		EntityName: strPtr(entityName),
		EntityType: strPtr("entity"),
		Program:    program,
		AddedAt:    &evt.OccurredAt,
	}); err != nil {
		log.Printf("[resolver] ofac sanction insert: %v", err)
	}

	return companyID, nil
}

// finnhubTrade mirrors the JSON stored by the Finnhub parser.
type finnhubTrade struct {
	Symbol string  `json:"s"`
	Price  float64 `json:"p"`
	Volume float64 `json:"v"`
}

func (r *Resolver) resolveFinnhub(ctx context.Context, evt db.Event) (int, error) {
	var trade finnhubTrade
	if err := json.Unmarshal(evt.EventData, &trade); err != nil {
		return 0, fmt.Errorf("unmarshal finnhub trade: %w", err)
	}
	if trade.Symbol == "" {
		return 0, nil
	}

	companyID, err := r.ensureCompany(ctx, trade.Symbol, "")
	if err != nil {
		return 0, err
	}
	if companyID == 0 {
		return 0, nil
	}

	// Extract typed market_data record.
	priceCents := int64(math.Round(trade.Price * 100))
	volume := int64(trade.Volume)

	if err := r.store.InsertMarketData(ctx, db.MarketDataRow{
		CompanyID:  companyID,
		Source:     "finnhub",
		DataType:   "trade",
		PriceCents: &priceCents,
		Volume:     &volume,
		RecordedAt: evt.OccurredAt,
	}); err != nil {
		// Duplicate inserts are expected during backfill — don't spam logs.
		if !isDuplicateError(err) {
			log.Printf("[resolver] finnhub market_data insert for %s: %v", trade.Symbol, err)
		}
	}

	return companyID, nil
}

// warnFiling mirrors the JSON stored by the WARN parser (camelCase keys).
type warnFiling struct {
	Company       string `json:"company"`
	City          string `json:"city"`
	State         string `json:"state"`
	Employees     int    `json:"employees_affected"`
	NoticeDate    string `json:"notice_date"`
	EffectiveDate string `json:"effective_date"`
}

func (r *Resolver) resolveWarn(ctx context.Context, evt db.Event) (int, error) {
	var filing warnFiling
	if err := json.Unmarshal(evt.EventData, &filing); err != nil {
		return 0, fmt.Errorf("unmarshal warn filing: %w", err)
	}

	var companyID int
	if filing.Company != "" {
		cid, err := r.ensureCompany(ctx, "", filing.Company)
		if err != nil {
			return 0, err
		}
		companyID = cid
	}

	var companyIDPtr *int
	if companyID > 0 {
		companyIDPtr = &companyID
	}
	eventID := evt.ID
	state := filing.State
	city := filing.City
	workers := filing.Employees

	var layoffDate *time.Time
	if filing.EffectiveDate != "" {
		if t, err := time.Parse("01/02/2006", filing.EffectiveDate); err == nil {
			ts := t.UTC()
			layoffDate = &ts
		}
	}
	var filedAt *time.Time
	if filing.NoticeDate != "" {
		if t, err := time.Parse("01/02/2006", filing.NoticeDate); err == nil {
			ts := t.UTC()
			filedAt = &ts
		}
	}

	if err := r.store.InsertWarnFiling(ctx, db.WarnFiling{
		EventID:         &eventID,
		CompanyID:       companyIDPtr,
		State:           &state,
		City:            &city,
		WorkersAffected: &workers,
		LayoffDate:      layoffDate,
		FiledAt:         filedAt,
	}); err != nil {
		if !isDuplicateError(err) {
			log.Printf("[resolver] warn filing insert for %s: %v", filing.Company, err)
		}
	}

	return companyID, nil
}

// Backfill processes all unresolved events for a given source (or all sources
// if source is empty). It processes in batches, paginated by event ID to avoid
// reprocessing events that remain unresolved. Returns total resolved count.
func (r *Resolver) Backfill(ctx context.Context, source string) (int, error) {
	const batchSize = 500
	totalResolved := 0
	lastID := 0

	for {
		if ctx.Err() != nil {
			return totalResolved, ctx.Err()
		}

		events, err := r.store.ListUnresolvedEventsAfter(ctx, source, lastID, batchSize)
		if err != nil {
			return totalResolved, fmt.Errorf("listing unresolved events: %w", err)
		}
		if len(events) == 0 {
			break
		}

		batchResolved := 0
		for _, evt := range events {
			if cid := r.Resolve(ctx, evt); cid > 0 {
				batchResolved++
			}
			if evt.ID > lastID {
				lastID = evt.ID
			}
		}

		totalResolved += batchResolved
		log.Printf("[resolver] backfill batch: %d/%d resolved (total: %d)",
			batchResolved, len(events), totalResolved)

		if len(events) < batchSize {
			break
		}
	}

	return totalResolved, nil
}

// normalizeName strips common corporate suffixes for fuzzy matching.
// Input should already be lowercase.
func normalizeName(name string) string {
	suffixes := []string{
		" incorporated", " inc.", " inc", " corporation", " corp.", " corp",
		" limited", " ltd.", " ltd", " company", " co.", " co",
		" holdings", " holding", " group", " plc", " se", " sa", " nv",
		" class a", " class b", " class c",
		",", ".", " ",
	}
	result := name
	for _, suf := range suffixes {
		result = strings.TrimSuffix(result, suf)
	}
	return strings.TrimSpace(result)
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// isDuplicateError checks if the error is a unique constraint violation
// (works for both PostgreSQL and SQLite).
func isDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "23505") ||
		strings.Contains(msg, "UNIQUE constraint failed")
}
