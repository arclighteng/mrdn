package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/spf13/cobra"
)

// Ingest US House congressional stock trades from a House-Stock-Watcher-format
// JSON file or URL. The HSW project (housestockwatcher.com) is no longer
// maintained, but Wayback Machine has a Nov 2024 snapshot of their full
// 17k-trade dataset that we can replay.
//
// Default URL points at the Wayback snapshot. Use --file to point at a local
// JSON file you've already downloaded.
//
// Each row produces:
//   - one events row (source=house_clerk_ptr)
//   - one congressional_trades row (linked to person + company if both resolve)
//   - upserts a person row for the representative if not already known
//
// Existing high-tier seed persons are NOT downgraded — if Pelosi is tier 1,
// she stays tier 1.

const defaultHouseTradesURL = "http://web.archive.org/web/20241129040416/https://house-stock-watcher-data.s3-us-west-2.amazonaws.com/data/all_transactions.json"

var (
	houseTradesFile  string
	houseTradesURL   string
	houseTradesLimit int
)

type hswRecord struct {
	DisclosureYear   int    `json:"disclosure_year"`
	DisclosureDate   string `json:"disclosure_date"`   // MM/DD/YYYY
	TransactionDate  string `json:"transaction_date"`  // YYYY-MM-DD
	Owner            string `json:"owner"`
	Ticker           string `json:"ticker"`
	AssetDescription string `json:"asset_description"`
	Type             string `json:"type"` // purchase, sale_full, sale_partial, exchange
	Amount           string `json:"amount"`
	Representative   string `json:"representative"`
	District         string `json:"district"`
	State            string `json:"state"`
	PtrLink          string `json:"ptr_link"`
	Industry         string `json:"industry"`
	Sector           string `json:"sector"`
	Party            string `json:"party"`
}

var ingestHouseTradesCmd = &cobra.Command{
	Use:   "ingest-house-trades",
	Short: "Ingest US House congressional stock trades from a HSW-format JSON source",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		d, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer d.Close()
		store := db.NewStore(d)

		startTime := time.Now()
		var httpCode int
		var runErr error
		var stats struct {
			events, trades, personsNew, companyMatched, skippedNoTicker, skippedDup, errors int
		}
		defer func() {
			errStr := ""
			if runErr != nil {
				errStr = runErr.Error()
			}
			_ = store.RecordIngestAttempt(context.Background(), db.IngestAttempt{
				Source:     "house_clerk_ptr",
				Success:    runErr == nil,
				HTTPCode:   httpCode,
				Error:      errStr,
				Records:    stats.events,
				DurationMs: int(time.Since(startTime).Milliseconds()),
				HasNewData: stats.events > 0,
			})
		}()

		// Load JSON
		var raw []byte
		if houseTradesFile != "" {
			log.Printf("ingest-house-trades: reading local file %s", houseTradesFile)
			raw, err = os.ReadFile(houseTradesFile)
			if err != nil {
				runErr = fmt.Errorf("reading file: %w", err)
				return runErr
			}
		} else {
			log.Printf("ingest-house-trades: fetching %s", houseTradesURL)
			req, _ := http.NewRequestWithContext(ctx, "GET", houseTradesURL, nil)
			req.Header.Set("User-Agent", "mrdn-ingester/1.0")
			resp, ferr := (&http.Client{Timeout: 120 * time.Second}).Do(req)
			if ferr != nil {
				runErr = fmt.Errorf("http: %w", ferr)
				return runErr
			}
			defer resp.Body.Close()
			httpCode = resp.StatusCode
			if resp.StatusCode != 200 {
				runErr = fmt.Errorf("http status %d", resp.StatusCode)
				return runErr
			}
			raw, err = io.ReadAll(resp.Body)
			if err != nil {
				runErr = fmt.Errorf("reading response: %w", err)
				return runErr
			}
		}
		log.Printf("ingest-house-trades: %d bytes loaded", len(raw))

		var records []hswRecord
		if err := json.Unmarshal(raw, &records); err != nil {
			runErr = fmt.Errorf("parsing JSON: %w", err)
			return runErr
		}
		log.Printf("ingest-house-trades: %d records to process", len(records))

		if houseTradesLimit > 0 && houseTradesLimit < len(records) {
			records = records[:houseTradesLimit]
			log.Printf("ingest-house-trades: limited to first %d", houseTradesLimit)
		}

		personCache := map[string]int{} // slug -> id

		for i, r := range records {
			if ctx.Err() != nil {
				log.Printf("ingest-house-trades: cancelled at record %d", i)
				break
			}
			if i > 0 && i%500 == 0 {
				log.Printf("ingest-house-trades: progress %d/%d (events=%d trades=%d)", i, len(records), stats.events, stats.trades)
			}

			// Resolve person (cache by slug, no-clobber for existing)
			personID, created, perr := resolvePerson(ctx, store, &r, personCache)
			if perr != nil {
				stats.errors++
				continue
			}
			if created {
				stats.personsNew++
			}

			// Build event
			eventData, _ := json.Marshal(r)
			occurredAt := parseDate(r.DisclosureDate, r.TransactionDate)
			sourceID := strings.TrimSpace(r.PtrLink) + "|" + r.Ticker + "|" + r.TransactionDate + "|" + r.Type
			ev := db.Event{
				Source:     "house_clerk_ptr",
				SourceID:   &sourceID,
				EventType:  "congressional_trade",
				EventData:  eventData,
				OccurredAt: occurredAt,
			}

			eventID, ierr := store.InsertEvent(ctx, ev)
			if ierr != nil {
				// InsertEvent returns 0 (no error) for duplicates when using
				// INSERT OR IGNORE. A non-nil error here is a genuine failure.
				if strings.Contains(ierr.Error(), "UNIQUE constraint failed") {
					stats.skippedDup++
				} else {
					stats.errors++
					if stats.errors <= 5 {
						log.Printf("ingest-house-trades: insert event error: %v", ierr)
					}
				}
				continue
			}
			stats.events++

			// Resolve company by ticker (best effort)
			var companyID *int
			ticker := strings.ToUpper(strings.TrimSpace(r.Ticker))
			if ticker != "" && ticker != "--" {
				c, cerr := store.GetCompanyByTicker(ctx, ticker)
				if cerr == nil {
					id := c.ID
					companyID = &id
					stats.companyMatched++
					// Backfill name from asset_description when current name is just the ticker
					desc := strings.TrimSpace(r.AssetDescription)
					if c.Name == c.Ticker && desc != "" && desc != ticker && desc != "--" {
						store.DB().ExecContext(ctx, `UPDATE companies SET name = ? WHERE ticker = ? AND name = ticker`, desc, ticker)
					}
				}
			} else {
				stats.skippedNoTicker++
			}

			// Insert typed trade row
			low, high := parseAmountRange(r.Amount)
			td := parseISODate(r.TransactionDate)
			fd := parseUSDate(r.DisclosureDate)
			owner := r.Owner
			tradeType := r.Type
			tk := ticker
			pidCopy := personID
			ct := db.CongressionalTrade{
				EventID:         &eventID,
				PersonID:        &pidCopy,
				CompanyID:       companyID,
				OwnerType:       &owner,
				Ticker:          &tk,
				TradeType:       &tradeType,
				AmountRangeLow:  low,
				AmountRangeHigh: high,
				FiledAt:         fd,
				TradedAt:        td,
			}
			if terr := store.InsertCongressionalTrade(ctx, ct); terr != nil {
				stats.errors++
				continue
			}
			stats.trades++
		}

		log.Printf("ingest-house-trades: done — events=%d trades=%d new_persons=%d company_matched=%d skipped_no_ticker=%d skipped_dup=%d errors=%d",
			stats.events, stats.trades, stats.personsNew, stats.companyMatched, stats.skippedNoTicker, stats.skippedDup, stats.errors)
		return nil
	},
}

// resolvePerson returns the person_id for a representative, creating one if
// not already present. Existing persons are NEVER overwritten (so seeded
// tier-1 figures keep their tier).
func resolvePerson(ctx context.Context, store *db.Store, r *hswRecord, cache map[string]int) (int, bool, error) {
	slug := slugify(r.Representative)
	if id, ok := cache[slug]; ok {
		return id, false, nil
	}
	// try fetch existing — never clobber an existing person row
	p, err := store.GetPersonBySlug(ctx, slug)
	if err == nil {
		cache[slug] = p.ID
		return p.ID, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) && !strings.Contains(err.Error(), "no rows") {
		return 0, false, err
	}
	state := r.State
	if state == "" && len(r.District) >= 2 {
		state = r.District[:2]
	}
	party := normalizeParty(r.Party)
	branch := "legislative"
	role := "representative"
	tier := 3
	source := "house_clerk_ptr"
	np := db.Person{
		Slug:             slug,
		Name:             cleanName(r.Representative),
		Role:             role,
		Tier:             tier,
		Branch:           &branch,
		State:            stringOrNil(state),
		Party:            stringOrNil(party),
		DisclosureSource: &source,
	}
	out, uerr := store.UpsertPerson(ctx, np)
	if uerr != nil {
		return 0, false, uerr
	}
	cache[slug] = out.ID
	return out.ID, true, nil
}

var (
	nonAlnum    = regexp.MustCompile(`[^a-z0-9]+`)
	titlePrefix = regexp.MustCompile(`(?i)^(hon\.?|mr\.?|mrs\.?|ms\.?|dr\.?|rep\.?|sen\.?)\s+`)
	amountNumRe = regexp.MustCompile(`[\d,]+`)
)

func cleanName(n string) string {
	n = strings.TrimSpace(n)
	for {
		s := titlePrefix.ReplaceAllString(n, "")
		if s == n {
			break
		}
		n = strings.TrimSpace(s)
	}
	return n
}

func slugify(n string) string {
	n = cleanName(n)
	n = strings.ToLower(n)
	n = nonAlnum.ReplaceAllString(n, "-")
	n = strings.Trim(n, "-")
	if n == "" {
		n = "unknown"
	}
	return n
}

func normalizeParty(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "republican", "r":
		return "R"
	case "democratic", "democrat", "d":
		return "D"
	case "independent", "i":
		return "I"
	}
	return ""
}

func stringOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func parseAmountRange(s string) (*int, *int) {
	// "$1,001 - $15,000"
	matches := amountNumRe.FindAllString(s, -1)
	if len(matches) == 0 {
		return nil, nil
	}
	parse := func(x string) *int {
		v, err := strconv.Atoi(strings.ReplaceAll(x, ",", ""))
		if err != nil {
			return nil
		}
		return &v
	}
	if len(matches) == 1 {
		v := parse(matches[0])
		return v, v
	}
	return parse(matches[0]), parse(matches[1])
}

func parseUSDate(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse("01/02/2006", s)
	if err != nil {
		return nil
	}
	return &t
}

func parseISODate(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil
	}
	return &t
}

func parseDate(us, iso string) time.Time {
	if t := parseISODate(iso); t != nil {
		return *t
	}
	if t := parseUSDate(us); t != nil {
		return *t
	}
	return time.Now().UTC()
}

func init() {
	ingestHouseTradesCmd.Flags().StringVar(&houseTradesFile, "file", "", "local JSON file path (HSW format)")
	ingestHouseTradesCmd.Flags().StringVar(&houseTradesURL, "url", defaultHouseTradesURL, "URL to fetch (HSW format JSON)")
	ingestHouseTradesCmd.Flags().IntVar(&houseTradesLimit, "limit", 0, "process only first N records (0 = all)")
	rootCmd.AddCommand(ingestHouseTradesCmd)
}
