package cli

import (
	"context"
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

var generateAliasesCmd = &cobra.Command{
	Use:   "generate-aliases",
	Short: "Seed entity_aliases from existing company data",
	Long: `Generates company aliases from the companies table:
- Ticker as alias for the company name
- Common abbreviations and DBA names
- Logs top unresolved event company names for manual review.`,
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

		companies, err := store.ListAllCompanyLookups(ctx)
		if err != nil {
			return fmt.Errorf("listing companies: %w", err)
		}

		var inserted, skipped int
		for _, c := range companies {
			aliases := generateCompanyAliases(c)
			for _, alias := range aliases {
				result, err := store.InsertEntityAlias(ctx, db.EntityAlias{
					EntityID:   c.ID,
					EntityType: "company",
					Alias:      alias,
					Source:     strRef("generate-aliases"),
				})
				if err != nil {
					log.Printf("[generate-aliases] error inserting alias %q for %s: %v", alias, c.Ticker, err)
					continue
				}
				if result.ID == 0 {
					skipped++ // duplicate
				} else {
					inserted++
				}
			}
		}

		log.Printf("[generate-aliases] done — %d aliases inserted, %d duplicates skipped", inserted, skipped)
		return nil
	},
}

func strRef(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// generateCompanyAliases produces alias variants for a company.
func generateCompanyAliases(c db.CompanyLookup) []string {
	aliases := make([]string, 0, 4)

	name := c.Name
	ticker := c.Ticker

	// Ticker itself as an alias (e.g., "AAPL" → Apple Inc).
	aliases = append(aliases, ticker)

	// Name without common suffixes.
	suffixes := []string{
		" Inc.", " Inc", " Corporation", " Corp.", " Corp",
		" Limited", " Ltd.", " Ltd", " Company", " Co.",
		" Holdings", " Holding", " Group", " PLC", " SE", " SA", " NV",
		", Inc.", ", Inc", ", LLC", ", Ltd.",
	}
	stripped := name
	lowerName := strings.ToLower(name)
	for _, suf := range suffixes {
		lowerSuf := strings.ToLower(suf)
		if strings.HasSuffix(lowerName, lowerSuf) {
			stripped = name[:len(name)-len(suf)]
			break
		}
	}
	if stripped != name && stripped != ticker {
		aliases = append(aliases, stripped)
	}

	// Uppercase version of full name (for OFAC-style names).
	upper := strings.ToUpper(name)
	if upper != name {
		aliases = append(aliases, upper)
	}

	return aliases
}

func init() {
	rootCmd.AddCommand(generateAliasesCmd)
}
