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
	"strings"
	"syscall"
	"time"

	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/spf13/cobra"
)

// Ingest US Senate congressional stock trades from the Senate Stock Watcher
// GitHub JSON dump. The dataset is maintained at:
// https://github.com/timothycarambat/senate-stock-watcher-data
//
// Each row produces:
//   - one events row (source=senate_stock_watcher)
//   - one congressional_trades row (linked to person + company if resolved)
//   - upserts a person row for the senator if not already known
//
// Existing high-tier seed persons are NOT downgraded.

const defaultSenateTradesURL = "https://raw.githubusercontent.com/timothycarambat/senate-stock-watcher-data/master/aggregate/all_transactions.json"

var (
	senateTradesFile  string
	senateTradesURL   string
	senateTradesLimit int
)

type sswRecord struct {
	TransactionDate  string `json:"transaction_date"`  // MM/DD/YYYY
	Owner            string `json:"owner"`
	Ticker           string `json:"ticker"`
	AssetDescription string `json:"asset_description"`
	AssetType        string `json:"asset_type"`
	Type             string `json:"type"` // Purchase, Sale (Full), Sale (Partial), Exchange
	Amount           string `json:"amount"`
	Comment          string `json:"comment"`
	Senator          string `json:"senator"`
	PtrLink          string `json:"ptr_link"`
}

// senateFullName returns the senator's display name from the record.
func senateFullName(r *sswRecord) string {
	name := strings.TrimSpace(r.Senator)
	if name == "" {
		return "Unknown"
	}
	return name
}

// resolveSenator returns the person_id for a senator, creating one if not
// already present. Existing persons are NEVER overwritten (so seeded tier-1
// figures keep their tier).
func resolveSenator(ctx context.Context, store *db.Store, r *sswRecord, cache map[string]int) (int, bool, error) {
	fullName := senateFullName(r)
	slug := slugify(fullName)
	if id, ok := cache[slug]; ok {
		return id, false, nil
	}
	// Try fetch existing -- never clobber an existing person row
	p, err := store.GetPersonBySlug(ctx, slug)
	if err == nil {
		cache[slug] = p.ID
		return p.ID, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) && !strings.Contains(err.Error(), "no rows") {
		return 0, false, err
	}
	branch := "legislative"
	role := "senator"
	tier := 3
	source := "senate_stock_watcher"
	np := db.Person{
		Slug:             slug,
		Name:             cleanName(fullName),
		Role:             role,
		Tier:             tier,
		Branch:           &branch,
		DisclosureSource: &source,
	}
	out, uerr := store.UpsertPerson(ctx, np)
	if uerr != nil {
		return 0, false, uerr
	}
	cache[slug] = out.ID
	return out.ID, true, nil
}

// senateSourceID builds a deduplication key from the record fields.
func senateSourceID(r *sswRecord) string {
	return strings.TrimSpace(r.PtrLink) + "|" +
		strings.TrimSpace(r.Ticker) + "|" +
		strings.TrimSpace(r.TransactionDate) + "|" +
		strings.TrimSpace(r.Type)
}

var ingestSenateTradesCmd = &cobra.Command{
	Use:   "ingest-senate-trades",
	Short: "Ingest US Senate congressional stock trades from Senate Stock Watcher JSON",
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
				Source:     "senate_stock_watcher",
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
		if senateTradesFile != "" {
			log.Printf("ingest-senate-trades: reading local file %s", senateTradesFile)
			raw, err = os.ReadFile(senateTradesFile)
			if err != nil {
				runErr = fmt.Errorf("reading file: %w", err)
				return runErr
			}
		} else {
			log.Printf("ingest-senate-trades: fetching %s", senateTradesURL)
			req, _ := http.NewRequestWithContext(ctx, "GET", senateTradesURL, nil)
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
		log.Printf("ingest-senate-trades: %d bytes loaded", len(raw))

		var records []sswRecord
		if err := json.Unmarshal(raw, &records); err != nil {
			runErr = fmt.Errorf("parsing JSON: %w", err)
			return runErr
		}
		log.Printf("ingest-senate-trades: %d records to process", len(records))

		if senateTradesLimit > 0 && senateTradesLimit < len(records) {
			records = records[:senateTradesLimit]
			log.Printf("ingest-senate-trades: limited to first %d", senateTradesLimit)
		}

		personCache := map[string]int{} // slug -> id
		seenSourceIDs := map[string]bool{}

		for i, r := range records {
			if ctx.Err() != nil {
				log.Printf("ingest-senate-trades: cancelled at record %d", i)
				break
			}
			if i > 0 && i%500 == 0 {
				log.Printf("ingest-senate-trades: progress %d/%d (events=%d trades=%d)", i, len(records), stats.events, stats.trades)
			}

			// Deduplicate within the current batch
			srcID := senateSourceID(&r)
			if seenSourceIDs[srcID] {
				stats.skippedDup++
				continue
			}
			seenSourceIDs[srcID] = true

			// Resolve person (cache by slug, no-clobber for existing)
			personID, created, perr := resolveSenator(ctx, store, &r, personCache)
			if perr != nil {
				stats.errors++
				continue
			}
			if created {
				stats.personsNew++
			}

			// Build event
			eventData, _ := json.Marshal(r)
			occurredAt := parseDate(r.TransactionDate, "")
			ev := db.Event{
				Source:     "senate_stock_watcher",
				SourceID:   &srcID,
				EventType:  "congressional_trade",
				EventData:  eventData,
				OccurredAt: occurredAt,
			}

			eventID, ierr := store.InsertEvent(ctx, ev)
			if ierr != nil {
				if strings.Contains(ierr.Error(), "UNIQUE constraint failed") {
					stats.skippedDup++
				} else {
					stats.errors++
					if stats.errors <= 5 {
						log.Printf("ingest-senate-trades: insert event error: %v", ierr)
					}
				}
				continue
			}
			stats.events++

			// Resolve company by ticker (best effort)
			var companyID *int
			ticker := strings.ToUpper(strings.TrimSpace(r.Ticker))
			if ticker != "" && ticker != "--" && ticker != "N/A" {
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
			td := parseUSDate(r.TransactionDate)
			var fd *time.Time
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

		log.Printf("ingest-senate-trades: done — events=%d trades=%d new_persons=%d company_matched=%d skipped_no_ticker=%d skipped_dup=%d errors=%d",
			stats.events, stats.trades, stats.personsNew, stats.companyMatched, stats.skippedNoTicker, stats.skippedDup, stats.errors)
		return nil
	},
}

func init() {
	ingestSenateTradesCmd.Flags().StringVar(&senateTradesFile, "file", "", "local JSON file path (Senate Stock Watcher format)")
	ingestSenateTradesCmd.Flags().StringVar(&senateTradesURL, "url", defaultSenateTradesURL, "URL to fetch (Senate Stock Watcher format JSON)")
	ingestSenateTradesCmd.Flags().IntVar(&senateTradesLimit, "limit", 0, "process only first N records (0 = all)")
	rootCmd.AddCommand(ingestSenateTradesCmd)
}
