package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"regexp"
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
	InsertCongressionalTrade(ctx context.Context, t db.CongressionalTrade) error
	InsertCourtFiling(ctx context.Context, cf db.CourtFiling) error
	InsertTariff(ctx context.Context, t db.Tariff) error
	GetCompanyByAlias(ctx context.Context, alias string) (db.CompanyLookup, error)
	InsertEntityAlias(ctx context.Context, a db.EntityAlias) (db.EntityAlias, error)
	GetPersonBySlug(ctx context.Context, slug string) (db.Person, error)
	UpsertPerson(ctx context.Context, p db.Person) (db.Person, error)
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

// lookupByAlias queries the entity_aliases table for a case-insensitive match.
// On hit, caches the result in byNameLower. Returns 0 if no alias matches.
func (r *Resolver) lookupByAlias(ctx context.Context, name string) int {
	c, err := r.store.GetCompanyByAlias(ctx, name)
	if err != nil {
		return 0
	}
	r.mu.Lock()
	r.byNameLower[strings.ToLower(name)] = c.ID
	r.mu.Unlock()
	return c.ID
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

	// If the caller provided a real name (not empty, not just the ticker),
	// insert it as an entity alias so future name-based lookups can match.
	r.maybeInsertAlias(ctx, company.ID, name)

	return company.ID, nil
}

// maybeInsertAlias inserts a company name as an entity_alias if it is a
// meaningful name (non-empty and different from the ticker). Errors are
// logged but not propagated — alias insertion is best-effort.
func (r *Resolver) maybeInsertAlias(ctx context.Context, companyID int, name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	// Skip if the name is just the ticker symbol (case-insensitive).
	r.mu.RLock()
	// Check if any ticker in the cache matches the name.
	_, isTicker := r.byTicker[strings.ToUpper(name)]
	r.mu.RUnlock()
	if isTicker {
		return
	}

	if _, err := r.store.InsertEntityAlias(ctx, db.EntityAlias{
		EntityID:   companyID,
		EntityType: "company",
		Alias:      name,
		Source:     strPtr("resolver"),
	}); err != nil {
		log.Printf("[resolver] insert alias %q for company %d: %v", name, companyID, err)
	}

	// Also cache the alias in-memory for immediate use.
	r.mu.Lock()
	r.byNameLower[strings.ToLower(name)] = companyID
	r.mu.Unlock()
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
		companyID, err = r.resolveFedRegTariff(ctx, evt)
	case "efds_senate":
		companyID, err = r.resolveEFDSTrades(ctx, evt)
	case "finnhub_congress":
		companyID, err = r.resolveFinnhubCongress(ctx, evt)
	case "fmp_congress":
		companyID, err = r.resolveFMPCongress(ctx, evt)
	case "lambda_congress":
		companyID, err = r.resolveLambdaCongress(ctx, evt)
	case "courtlistener":
		companyID, err = r.resolveCourtListener(ctx, evt)
	case "finnhub":
		companyID, err = r.resolveFinnhub(ctx, evt)
	case "warn":
		companyID, err = r.resolveWarn(ctx, evt)
	case "sec_edgar_lit":
		companyID, err = r.resolveSecLitigation(ctx, evt)
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
		if companyID == 0 {
			companyID = r.lookupByAlias(ctx, companyName)
		}
		// Try the normalized (suffix-stripped) form against aliases too.
		if companyID == 0 {
			normalized := normalizeName(strings.ToLower(companyName))
			if normalized != strings.ToLower(companyName) {
				companyID = r.lookupByAlias(ctx, normalized)
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
		companyID = r.lookupByAlias(ctx, employer)
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
		companyID = r.lookupByAlias(ctx, recipient)
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
	if companyID == 0 {
		companyID = r.lookupByAlias(ctx, entityName)
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

// finnhubCongressEvent mirrors the JSON stored per-trade by the Finnhub
// congressional-trading parser (one event per trade record).
type finnhubCongressEvent struct {
	AmountFrom      float64 `json:"amountFrom"`
	AmountTo        float64 `json:"amountTo"`
	AssetName       string  `json:"assetName"`
	FilingDate      string  `json:"filingDate"`
	Name            string  `json:"name"`
	OwnerType       string  `json:"ownerType"`
	Position        string  `json:"position"`
	Symbol          string  `json:"symbol"`
	TransactionDate string  `json:"transactionDate"`
	TransactionType string  `json:"transactionType"`
}

// slugifyName converts a full name like "Nancy Pelosi" into a slug like
// "nancy-pelosi", matching the format used in the persons table.
func slugifyName(name string) string {
	var b strings.Builder
	prevHyphen := true // suppress leading hyphens
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevHyphen = false
		} else if !prevHyphen {
			b.WriteByte('-')
			prevHyphen = true
		}
	}
	// Trim trailing hyphen.
	s := b.String()
	return strings.TrimRight(s, "-")
}

func (r *Resolver) resolveFinnhubCongress(ctx context.Context, evt db.Event) (int, error) {
	var trade finnhubCongressEvent
	if err := json.Unmarshal(evt.EventData, &trade); err != nil {
		return 0, fmt.Errorf("unmarshal finnhub_congress trade: %w", err)
	}

	ticker := strings.ToUpper(strings.TrimSpace(trade.Symbol))
	if ticker == "" || ticker == "--" || ticker == "N/A" {
		return 0, nil
	}

	companyID, err := r.ensureCompany(ctx, ticker, trade.AssetName)
	if err != nil {
		return 0, err
	}

	var companyIDPtr *int
	if companyID > 0 {
		companyIDPtr = &companyID
	}

	var personID *int
	if trade.Name != "" {
		slug := slugifyName(trade.Name)
		if p, err := r.store.GetPersonBySlug(ctx, slug); err == nil {
			personID = &p.ID
		}
	}

	var tradedAt *time.Time
	if trade.TransactionDate != "" {
		if t, err := time.Parse("2006-01-02", trade.TransactionDate); err == nil {
			tt := t.UTC()
			tradedAt = &tt
		}
	}

	var filedAt *time.Time
	if trade.FilingDate != "" {
		if t, err := time.Parse("2006-01-02", trade.FilingDate); err == nil {
			ft := t.UTC()
			filedAt = &ft
		}
	}

	eventID := evt.ID
	ct := db.CongressionalTrade{
		EventID:         &eventID,
		PersonID:        personID,
		CompanyID:       companyIDPtr,
		OwnerType:       strPtr(trade.OwnerType),
		Ticker:          strPtr(ticker),
		TradeType:       strPtr(trade.TransactionType),
		AmountRangeLow:  intPtr(int(trade.AmountFrom)),
		AmountRangeHigh: intPtr(int(trade.AmountTo)),
		FiledAt:         filedAt,
		TradedAt:        tradedAt,
	}

	if err := r.store.InsertCongressionalTrade(ctx, ct); err != nil {
		if !isDuplicateError(err) {
			log.Printf("[resolver] finnhub_congress congressional_trade insert: %v", err)
		}
	}

	return companyID, nil
}

// fmpCongressEvent mirrors the JSON stored per-trade by the FMP congressional
// trading parser (one event per trade record).
type fmpCongressEvent struct {
	Symbol           string `json:"symbol"`
	DisclosureDate   string `json:"disclosure_date"`
	TransactionDate  string `json:"transaction_date"`
	FirstName        string `json:"first_name"`
	LastName         string `json:"last_name"`
	Office           string `json:"office"`
	District         string `json:"district"`
	Owner            string `json:"owner"`
	AssetDescription string `json:"asset_description"`
	AssetType        string `json:"asset_type"`
	TradeType        string `json:"trade_type"`
	Amount           string `json:"amount"`
	AmountLow        int    `json:"amount_low"`
	AmountHigh       int    `json:"amount_high"`
	Chamber          string `json:"chamber"` // "senate" or "house"
	Link             string `json:"link"`
}

func (r *Resolver) resolveFMPCongress(ctx context.Context, evt db.Event) (int, error) {
	var trade fmpCongressEvent
	if err := json.Unmarshal(evt.EventData, &trade); err != nil {
		return 0, fmt.Errorf("unmarshal fmp_congress trade: %w", err)
	}

	ticker := strings.ToUpper(strings.TrimSpace(trade.Symbol))
	if ticker == "" || ticker == "--" || ticker == "N/A" {
		return 0, nil
	}

	companyID, err := r.ensureCompany(ctx, ticker, trade.AssetDescription)
	if err != nil {
		return 0, err
	}

	// If the asset description is a real name distinct from the ticker,
	// ensure it's registered as an alias for future name-based lookups.
	if companyID > 0 {
		desc := strings.TrimSpace(trade.AssetDescription)
		if desc != "" && !strings.EqualFold(desc, ticker) {
			r.maybeInsertAlias(ctx, companyID, desc)
		}
	}

	var companyIDPtr *int
	if companyID > 0 {
		companyIDPtr = &companyID
	}

	// Determine role from chamber.
	role := "senator"
	if trade.Chamber == "house" {
		role = "representative"
	}

	var personID *int
	fullName := strings.TrimSpace(trade.FirstName + " " + trade.LastName)
	if fullName != "" && fullName != " " {
		slug := slugifyName(fullName)
		if p, err := r.store.GetPersonBySlug(ctx, slug); err == nil {
			personID = &p.ID
		} else {
			// Person not found — create them from FMP data.
			branch := "legislative"
			state := trade.District
			p, err := r.store.UpsertPerson(ctx, db.Person{
				Slug:   slug,
				Name:   fullName,
				Role:   role,
				Tier:   2,
				Branch: &branch,
				State:  &state,
			})
			if err == nil {
				personID = &p.ID
			}
		}
	}

	var tradedAt *time.Time
	if trade.TransactionDate != "" {
		if t, err := time.Parse("2006-01-02", trade.TransactionDate); err == nil {
			tt := t.UTC()
			tradedAt = &tt
		}
	}

	var filedAt *time.Time
	if trade.DisclosureDate != "" {
		if t, err := time.Parse("2006-01-02", trade.DisclosureDate); err == nil {
			ft := t.UTC()
			filedAt = &ft
		}
	}

	eventID := evt.ID
	ct := db.CongressionalTrade{
		EventID:         &eventID,
		PersonID:        personID,
		CompanyID:       companyIDPtr,
		OwnerType:       strPtr(trade.Owner),
		Ticker:          strPtr(ticker),
		TradeType:       strPtr(trade.TradeType),
		AmountRangeLow:  intPtrOrNil(trade.AmountLow),
		AmountRangeHigh: intPtrOrNil(trade.AmountHigh),
		FiledAt:         filedAt,
		TradedAt:        tradedAt,
	}

	if err := r.store.InsertCongressionalTrade(ctx, ct); err != nil {
		if !isDuplicateError(err) {
			log.Printf("[resolver] fmp_congress congressional_trade insert: %v", err)
		}
	}

	return companyID, nil
}

// lambdaCongressEvent mirrors the JSON stored per-trade by the Lambda Finance
// congressional trading parser.
type lambdaCongressEvent struct {
	Symbol           string `json:"symbol"`
	Representative   string `json:"representative"`
	TransactionDate  string `json:"transaction_date"`
	DisclosureDate   string `json:"disclosure_date"`
	TradeType        string `json:"trade_type"`
	Amount           string `json:"amount"`
	AmountLow        int    `json:"amount_low"`
	AmountHigh       int    `json:"amount_high"`
	Chamber          string `json:"chamber"`
	Party            string `json:"party"`
	State            string `json:"state"`
	District         string `json:"district"`
	AssetDescription string `json:"asset_description"`
	Owner            string `json:"owner"`
	PtrLink          string `json:"ptr_link"`
}

func (r *Resolver) resolveLambdaCongress(ctx context.Context, evt db.Event) (int, error) {
	var trade lambdaCongressEvent
	if err := json.Unmarshal(evt.EventData, &trade); err != nil {
		return 0, fmt.Errorf("unmarshal lambda_congress trade: %w", err)
	}

	ticker := strings.ToUpper(strings.TrimSpace(trade.Symbol))
	if ticker == "" || ticker == "--" || ticker == "N/A" || ticker == "OT" {
		return 0, nil
	}

	companyID, err := r.ensureCompany(ctx, ticker, trade.AssetDescription)
	if err != nil {
		return 0, err
	}

	// If the asset description is a real name distinct from the ticker,
	// ensure it's registered as an alias for future name-based lookups.
	if companyID > 0 {
		desc := strings.TrimSpace(trade.AssetDescription)
		if desc != "" && !strings.EqualFold(desc, ticker) {
			r.maybeInsertAlias(ctx, companyID, desc)
		}
	}

	var companyIDPtr *int
	if companyID > 0 {
		companyIDPtr = &companyID
	}

	role := "senator"
	if trade.Chamber == "house" {
		role = "representative"
	}

	var personID *int
	fullName := strings.TrimSpace(trade.Representative)
	if fullName != "" {
		slug := slugifyName(fullName)
		if p, err := r.store.GetPersonBySlug(ctx, slug); err == nil {
			personID = &p.ID
		} else {
			branch := "legislative"
			state := trade.State
			p, err := r.store.UpsertPerson(ctx, db.Person{
				Slug:   slug,
				Name:   fullName,
				Role:   role,
				Tier:   2,
				Branch: &branch,
				State:  &state,
			})
			if err == nil {
				personID = &p.ID
			}
		}
	}

	var tradedAt *time.Time
	if trade.TransactionDate != "" {
		if t, err := time.Parse("2006-01-02", trade.TransactionDate); err == nil {
			tt := t.UTC()
			tradedAt = &tt
		}
	}

	var filedAt *time.Time
	if trade.DisclosureDate != "" {
		if t, err := time.Parse("2006-01-02", trade.DisclosureDate); err == nil {
			ft := t.UTC()
			filedAt = &ft
		}
	}

	eventID := evt.ID
	ct := db.CongressionalTrade{
		EventID:         &eventID,
		PersonID:        personID,
		CompanyID:       companyIDPtr,
		OwnerType:       strPtr(trade.Owner),
		Ticker:          strPtr(ticker),
		TradeType:       strPtr(trade.TradeType),
		AmountRangeLow:  intPtrOrNil(trade.AmountLow),
		AmountRangeHigh: intPtrOrNil(trade.AmountHigh),
		FiledAt:         filedAt,
		TradedAt:        tradedAt,
	}

	if err := r.store.InsertCongressionalTrade(ctx, ct); err != nil {
		if !isDuplicateError(err) {
			log.Printf("[resolver] lambda_congress congressional_trade insert: %v", err)
		}
	}

	return companyID, nil
}

// efdsDisclosure mirrors the JSON stored by the EFDS Senate parser.
type efdsDisclosure struct {
	FirstName    string            `json:"first_name"`
	LastName     string            `json:"last_name"`
	FilingType   string            `json:"filing_type"`
	FilingDate   string            `json:"filing_date"`
	ReportID     string            `json:"report_id"`
	Transactions []efdsTransaction `json:"transactions"`
}

type efdsTransaction struct {
	Ticker     string `json:"ticker"`
	TradeType  string `json:"trade_type"`
	AmountLow  int    `json:"amount_low"`
	AmountHigh int    `json:"amount_high"`
	Owner      string `json:"owner"`
	TradedAt   string `json:"traded_at"`
}

type secLitEvent struct {
	ID    string `json:"id"`
	Date  string `json:"date"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

type fedRegDoc struct {
	Type          string         `json:"type"`
	Title         string         `json:"title"`
	EffectiveOn   string         `json:"effective_on"`
	CFRReferences []cfrReference `json:"cfr_references"`
}

type cfrReference struct {
	Title int `json:"title"`
	Part  int `json:"part"`
}

var tariffCFRParts = map[int]bool{
	12: true, 134: true, 159: true, 163: true,
}

func (r *Resolver) resolveEFDSTrades(ctx context.Context, evt db.Event) (int, error) {
	var disc efdsDisclosure
	if err := json.Unmarshal(evt.EventData, &disc); err != nil {
		return 0, fmt.Errorf("unmarshal efds disclosure: %w", err)
	}

	if len(disc.Transactions) == 0 {
		return 0, nil
	}

	slug := strings.ToLower(strings.TrimSpace(disc.FirstName) + "-" + strings.TrimSpace(disc.LastName))
	slug = strings.ReplaceAll(slug, " ", "-")
	var personID *int
	if p, err := r.store.GetPersonBySlug(ctx, slug); err == nil {
		personID = &p.ID
	}

	var filedAt *time.Time
	if disc.FilingDate != "" {
		if t, err := time.Parse("01/02/2006", disc.FilingDate); err == nil {
			ft := t.UTC()
			filedAt = &ft
		}
	}

	var firstCompanyID int
	eventID := evt.ID
	for _, tx := range disc.Transactions {
		ticker := strings.ToUpper(strings.TrimSpace(tx.Ticker))
		if ticker == "" || ticker == "--" || ticker == "N/A" {
			continue
		}

		companyID, err := r.ensureCompany(ctx, ticker, "")
		if err != nil {
			log.Printf("[resolver] efds trade ticker %s: %v", ticker, err)
			continue
		}
		if firstCompanyID == 0 && companyID > 0 {
			firstCompanyID = companyID
		}

		var companyIDPtr *int
		if companyID > 0 {
			companyIDPtr = &companyID
		}

		var tradedAt *time.Time
		if tx.TradedAt != "" {
			if t, err := time.Parse("2006-01-02", tx.TradedAt); err == nil {
				tt := t.UTC()
				tradedAt = &tt
			}
		}

		trade := db.CongressionalTrade{
			EventID:         &eventID,
			PersonID:        personID,
			CompanyID:       companyIDPtr,
			OwnerType:       strPtr(tx.Owner),
			Ticker:          strPtr(ticker),
			TradeType:       strPtr(tx.TradeType),
			AmountRangeLow:  intPtr(tx.AmountLow),
			AmountRangeHigh: intPtr(tx.AmountHigh),
			FiledAt:         filedAt,
			TradedAt:        tradedAt,
		}

		if err := r.store.InsertCongressionalTrade(ctx, trade); err != nil {
			if !isDuplicateError(err) {
				log.Printf("[resolver] efds congressional_trade insert: %v", err)
			}
		}
	}

	return firstCompanyID, nil
}

func (r *Resolver) resolveSecLitigation(ctx context.Context, evt db.Event) (int, error) {
	var rel secLitEvent
	if err := json.Unmarshal(evt.EventData, &rel); err != nil {
		return 0, fmt.Errorf("unmarshal sec litigation: %w", err)
	}

	parties := extractParties(rel.Title)

	var companyID int
	for _, party := range parties {
		cid := r.lookupName(party)
		if cid == 0 {
			c, err := r.store.SearchCompanyByName(ctx, party)
			if err == nil && c != nil {
				cid = c.ID
				r.mu.Lock()
				r.byNameLower[strings.ToLower(party)] = cid
				r.mu.Unlock()
			}
		}
		if cid == 0 {
			cid = r.lookupByAlias(ctx, party)
		}
		if cid > 0 {
			companyID = cid
			break
		}
	}

	var companyIDPtr *int
	if companyID > 0 {
		companyIDPtr = &companyID
	}

	var filedAt *time.Time
	if rel.Date != "" {
		if t, err := time.Parse("2006-01-02", rel.Date); err == nil {
			ft := t.UTC()
			filedAt = &ft
		}
	}

	eventID := evt.ID
	if err := r.store.InsertCourtFiling(ctx, db.CourtFiling{
		EventID:    &eventID,
		CompanyID:  companyIDPtr,
		CaseNumber: strPtr(rel.ID),
		Court:      strPtr("SEC"),
		FilingType: strPtr("sec_litigation"),
		Parties:    parties,
		FiledAt:    filedAt,
	}); err != nil {
		if !isDuplicateError(err) {
			log.Printf("[resolver] sec litigation court_filing insert: %v", err)
		}
	}

	return companyID, nil
}

func extractParties(title string) []string {
	title = strings.TrimPrefix(title, "SEC ")
	for _, verb := range []string{
		"Charges ", "Files Action Against ", "Obtains ", "Announces ",
		"Settles With ", "Orders ", "Sues ",
	} {
		if strings.HasPrefix(title, verb) {
			title = strings.TrimPrefix(title, verb)
			break
		}
	}

	for _, sep := range []string{" for ", " with ", " in ", " Related to "} {
		if idx := strings.Index(title, sep); idx > 0 {
			title = title[:idx]
			break
		}
	}

	parts := strings.Split(title, " and ")
	parties := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			parties = append(parties, p)
		}
	}
	return parties
}

func (r *Resolver) resolveFedRegTariff(ctx context.Context, evt db.Event) (int, error) {
	var doc fedRegDoc
	if err := json.Unmarshal(evt.EventData, &doc); err != nil {
		return 0, fmt.Errorf("unmarshal federal register doc: %w", err)
	}

	if doc.Type != "Rule" && doc.Type != "Proposed Rule" {
		return 0, nil
	}

	isTariff := false
	for _, ref := range doc.CFRReferences {
		if ref.Title == 19 && tariffCFRParts[ref.Part] {
			isTariff = true
			break
		}
	}
	if !isTariff {
		return 0, nil
	}

	var effectiveAt *time.Time
	if doc.EffectiveOn != "" {
		if t, err := time.Parse("2006-01-02", doc.EffectiveOn); err == nil {
			et := t.UTC()
			effectiveAt = &et
		}
	}

	eventID := evt.ID
	if err := r.store.InsertTariff(ctx, db.Tariff{
		EventID:     &eventID,
		ActionType:  strPtr(doc.Title),
		EffectiveAt: effectiveAt,
	}); err != nil {
		if !isDuplicateError(err) {
			log.Printf("[resolver] federal_register tariff insert: %v", err)
		}
	}

	return 0, nil
}

func intPtr(n int) *int {
	return &n
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

// normalizeName strips common corporate suffixes and state-of-incorporation
// tags for fuzzy matching. Input should already be lowercase.
func normalizeName(name string) string {
	// Strip state-of-incorporation suffixes like "/de", "/md", "/nv".
	if idx := strings.LastIndex(name, "/"); idx > 0 {
		suffix := name[idx+1:]
		if len(suffix) <= 3 {
			name = strings.TrimSpace(name[:idx])
		}
	}

	suffixes := []string{
		" incorporated", " inc.", " inc", " corporation", " corp.", " corp",
		" limited", " ltd.", " ltd", " company", " co.", " co",
		" holdings", " holding", " group", " plc", " se", " sa", " nv",
		" l.l.c.", " llc", " l.p.", " lp",
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

// courtListenerEvent mirrors the JSON stored per-trade by the CourtListener
// judicial financial-disclosure parser (one event per investment record).
type courtListenerEvent struct {
	DisclosureID         int    `json:"disclosure_id"`
	InvestmentID         int    `json:"investment_id"`
	PersonURL            string `json:"person_url"`
	Year                 int    `json:"year"`
	Description          string `json:"description"`
	TransactionType      string `json:"transaction_type"`
	TransactionDate      string `json:"transaction_date"`
	TransactionValueCode string `json:"transaction_value_code"`
	GrossValueCode       string `json:"gross_value_code"`
}

// clTickerRe matches a parenthesised 1-5 letter ticker symbol, e.g. "(AAPL)".
var clTickerRe = regexp.MustCompile(`\(([A-Z]{1,5})\)`)

// extractTickerFromCLDesc extracts "AAPL" from "Apple Inc (AAPL) - Stock".
// Returns "" if no ticker found.
func extractTickerFromCLDesc(desc string) string {
	m := clTickerRe.FindStringSubmatch(desc)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// clPersonIDRe matches the numeric ID in a CourtListener people URL.
var clPersonIDRe = regexp.MustCompile(`/people/(\d+)/`)

// extractCLPersonID extracts "1234" from ".../people/1234/".
// Returns "" if the URL does not match the expected shape.
func extractCLPersonID(url string) string {
	m := clPersonIDRe.FindStringSubmatch(url)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// clValueRange decodes a CourtListener investment/transaction value code into
// an inclusive dollar range [low, high]. High is 0 when there is no upper bound.
func clValueRange(code string) (int, int) {
	switch code {
	case "J":
		return 15001, 50000
	case "K":
		return 50001, 100000
	case "L":
		return 100001, 250000
	case "M":
		return 250001, 500000
	case "N":
		return 500001, 1000000
	case "O":
		return 1000001, 5000000
	case "P1":
		return 5000001, 25000000
	case "P2":
		return 25000001, 50000000
	case "P3":
		return 50000001, 0
	default:
		return 0, 0
	}
}

// intPtrOrNil returns a pointer to v, or nil if v is 0.
func intPtrOrNil(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

func (r *Resolver) resolveCourtListener(ctx context.Context, evt db.Event) (int, error) {
	var trade courtListenerEvent
	if err := json.Unmarshal(evt.EventData, &trade); err != nil {
		return 0, fmt.Errorf("unmarshal courtlistener trade: %w", err)
	}

	ticker := extractTickerFromCLDesc(trade.Description)
	if ticker == "" {
		return 0, nil
	}

	companyID, err := r.ensureCompany(ctx, ticker, "")
	if err != nil {
		return 0, err
	}

	var companyIDPtr *int
	if companyID > 0 {
		companyIDPtr = &companyID
	}

	// Attempt to link to a person via the stable slug "judge-cl-<id>".
	// If the person hasn't been created yet (enrichment happens later), that's fine.
	var personID *int
	if personNumID := extractCLPersonID(trade.PersonURL); personNumID != "" {
		slug := "judge-cl-" + personNumID
		if p, err := r.store.GetPersonBySlug(ctx, slug); err == nil {
			personID = &p.ID
		}
	}

	var tradedAt *time.Time
	if trade.TransactionDate != "" {
		if t, err := time.Parse("2006-01-02", trade.TransactionDate); err == nil {
			tt := t.UTC()
			tradedAt = &tt
		}
	}

	// Prefer the transaction value code; fall back to gross value code.
	valueCode := trade.TransactionValueCode
	if valueCode == "" {
		valueCode = trade.GrossValueCode
	}
	amtLow, amtHigh := clValueRange(valueCode)

	eventID := evt.ID
	ct := db.CongressionalTrade{
		EventID:         &eventID,
		PersonID:        personID,
		CompanyID:       companyIDPtr,
		Ticker:          strPtr(ticker),
		TradeType:       strPtr(trade.TransactionType),
		AmountRangeLow:  intPtrOrNil(amtLow),
		AmountRangeHigh: intPtrOrNil(amtHigh),
		TradedAt:        tradedAt,
	}

	if err := r.store.InsertCongressionalTrade(ctx, ct); err != nil {
		if !isDuplicateError(err) {
			log.Printf("[resolver] courtlistener congressional_trade insert: %v", err)
		}
	}

	return companyID, nil
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
