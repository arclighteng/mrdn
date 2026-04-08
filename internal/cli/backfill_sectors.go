package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/spf13/cobra"
)

// backfill-sectors reads an HSW-format JSON file and updates companies.sector
// and companies.subsector for every ticker that appears in the file. HSW's
// raw sector taxonomy is noisy (mixes NASDAQ-era and GICS labels); we
// normalize to canonical GICS-11 + "Other" for display. The raw HSW sector is
// preserved as subsector so we don't lose fidelity.
//
// This command ALWAYS overwrites sector with the canonical value so repeated
// runs converge on the normalized taxonomy.

// hswToGICS maps HSW's raw sector labels to canonical GICS-11 sectors, plus
// "Other" for Miscellaneous. Unknown values pass through as "Other".
var hswToGICS = map[string]string{
	"Technology":             "Information Technology",
	"Telecommunications":     "Communication Services",
	"Health Care":            "Health Care",
	"Finance":                "Financials",
	"Energy":                 "Energy",
	"Real Estate":            "Real Estate",
	"Utilities":              "Utilities",
	"Public Utilities":       "Utilities",
	"Basic Materials":        "Materials",
	"Basic Industries":       "Materials",
	"Industrials":            "Industrials",
	"Capital Goods":          "Industrials",
	"Transportation":         "Industrials",
	"Consumer Discretionary": "Consumer Discretionary",
	"Consumer Services":      "Consumer Discretionary",
	"Consumer Durables":      "Consumer Discretionary",
	"Consumer Staples":       "Consumer Staples",
	"Consumer Non-Durables":  "Consumer Staples",
	"Miscellaneous":          "Other",
}

func normalizeSector(raw string) string {
	if raw == "" {
		return ""
	}
	if v, ok := hswToGICS[raw]; ok {
		return v
	}
	return "Other"
}

var backfillSectorsFile string

var backfillSectorsCmd = &cobra.Command{
	Use:   "backfill-sectors",
	Short: "Populate companies.sector/subsector from an HSW-format JSON file",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		pool, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer pool.Close()

		raw, err := os.ReadFile(backfillSectorsFile)
		if err != nil {
			return fmt.Errorf("reading file: %w", err)
		}
		var records []hswRecord
		if err := json.Unmarshal(raw, &records); err != nil {
			return fmt.Errorf("parsing JSON: %w", err)
		}

		// Collapse to unique ticker → (sector, industry). First non-empty wins.
		type meta struct{ sector, industry string }
		bySym := map[string]meta{}
		for _, r := range records {
			tk := strings.ToUpper(strings.TrimSpace(r.Ticker))
			if tk == "" || tk == "--" {
				continue
			}
			sec := strings.TrimSpace(r.Sector)
			ind := strings.TrimSpace(r.Industry)
			if sec == "" && ind == "" {
				continue
			}
			if existing, ok := bySym[tk]; ok {
				if existing.sector == "" {
					existing.sector = sec
				}
				if existing.industry == "" {
					existing.industry = ind
				}
				bySym[tk] = existing
				continue
			}
			bySym[tk] = meta{sector: sec, industry: ind}
		}
		log.Printf("backfill-sectors: %d unique tickers with sector/industry metadata", len(bySym))

		var updated, missing int
		for tk, m := range bySym {
			canonical := normalizeSector(m.sector)
			// Preserve the raw HSW sector as subsector when the record has no
			// industry, so we don't throw information away.
			sub := m.industry
			if sub == "" {
				sub = m.sector
			}
			ct, err := pool.Exec(ctx, `
				UPDATE companies
				SET sector = $2,
				    subsector = COALESCE(NULLIF($3,''), subsector)
				WHERE ticker = $1
			`, tk, nullIfEmpty(canonical), nullIfEmpty(sub))
			if err != nil {
				log.Printf("backfill-sectors: update %s: %v", tk, err)
				continue
			}
			if ct.RowsAffected() > 0 {
				updated++
			} else {
				missing++
			}
		}
		log.Printf("backfill-sectors: done — %d updated (normalized to GICS-11), %d ticker not in companies", updated, missing)
		return nil
	},
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func init() {
	backfillSectorsCmd.Flags().StringVar(&backfillSectorsFile, "file", "tmp/hsw.json", "HSW-format JSON file to read")
	rootCmd.AddCommand(backfillSectorsCmd)
}
