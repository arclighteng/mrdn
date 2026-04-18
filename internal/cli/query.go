package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/spf13/cobra"
)

var queryCmd = &cobra.Command{
	Use:   "query",
	Short: "Query the MRDN database",
}

var queryCompaniesCmd = &cobra.Command{
	Use:   "companies",
	Short: "Query companies with optional filters",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		ctx := context.Background()
		d, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer d.Close()

		store := db.NewStore(d)

		sector, _ := cmd.Flags().GetString("sector")
		minScore, _ := cmd.Flags().GetFloat64("min-score")
		limit, _ := cmd.Flags().GetInt("limit")

		filter := db.CompanyFilter{
			Sector: sector,
			Limit:  limit,
		}
		if minScore > 0 {
			filter.MinComposite = &minScore
		}

		companies, err := store.ListCompanies(ctx, filter)
		if err != nil {
			return fmt.Errorf("querying companies: %w", err)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "TICKER\tNAME\tSECTOR\tMARKET CAP")
		fmt.Fprintln(w, "------\t----\t------\t----------")
		for _, c := range companies {
			sector := "-"
			if c.Sector != nil {
				sector = *c.Sector
			}
			mcap := "-"
			if c.MarketCapBucket != nil {
				mcap = *c.MarketCapBucket
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", c.Ticker, c.Name, sector, mcap)
		}
		w.Flush()
		fmt.Fprintf(os.Stdout, "\n%d companies found\n", len(companies))
		return nil
	},
}

var queryEventsCmd = &cobra.Command{
	Use:   "events",
	Short: "Query events with optional filters",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		ctx := context.Background()
		d, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer d.Close()

		store := db.NewStore(d)

		source, _ := cmd.Flags().GetString("source")
		since, _ := cmd.Flags().GetString("since")
		limit, _ := cmd.Flags().GetInt("limit")

		filter := db.EventFilter{
			Source: source,
			Limit:  limit,
		}

		if since != "" {
			d, err := parseDuration(since)
			if err != nil {
				return fmt.Errorf("invalid --since value %q: %w", since, err)
			}
			t := time.Now().UTC().Add(-d)
			filter.Since = &t
		}

		events, err := store.ListEvents(ctx, filter)
		if err != nil {
			return fmt.Errorf("querying events: %w", err)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tSOURCE\tTYPE\tCOMPANY_ID\tOCCURRED_AT")
		fmt.Fprintln(w, "--\t------\t----\t----------\t-----------")
		for _, e := range events {
			cid := "-"
			if e.CompanyID != nil {
				cid = strconv.Itoa(*e.CompanyID)
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n",
				e.ID, e.Source, e.EventType, cid,
				e.OccurredAt.Format(time.RFC3339))
		}
		w.Flush()
		fmt.Fprintf(os.Stdout, "\n%d events found\n", len(events))
		return nil
	},
}

var queryConnectionsCmd = &cobra.Command{
	Use:   "connections [TICKER]",
	Short: "Show events connected to a company",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ticker := strings.ToUpper(args[0])

		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		ctx := context.Background()
		d, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer d.Close()

		store := db.NewStore(d)

		company, err := store.GetCompanyByTicker(ctx, ticker)
		if err != nil {
			return fmt.Errorf("looking up company %s: %w", ticker, err)
		}

		filter := db.EventFilter{
			CompanyID: &company.ID,
			Limit:     50,
		}
		events, err := store.ListEvents(ctx, filter)
		if err != nil {
			return fmt.Errorf("querying events for %s: %w", ticker, err)
		}

		fmt.Fprintf(os.Stdout, "Company: %s (%s)\n\n", company.Name, company.Ticker)

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tSOURCE\tTYPE\tOCCURRED_AT")
		fmt.Fprintln(w, "--\t------\t----\t-----------")
		for _, e := range events {
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\n",
				e.ID, e.Source, e.EventType,
				e.OccurredAt.Format(time.RFC3339))
		}
		w.Flush()
		fmt.Fprintf(os.Stdout, "\n%d events connected\n", len(events))
		return nil
	},
}

// parseDuration parses human-friendly durations like "24h", "7d", "30m".
func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("invalid day duration: %w", err)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func init() {
	rootCmd.AddCommand(queryCmd)
	queryCmd.AddCommand(queryCompaniesCmd)
	queryCmd.AddCommand(queryEventsCmd)
	queryCmd.AddCommand(queryConnectionsCmd)

	queryCompaniesCmd.Flags().String("sector", "", "Filter by sector")
	queryCompaniesCmd.Flags().Float64("min-score", 0, "Minimum composite score")
	queryCompaniesCmd.Flags().Int("limit", 50, "Maximum number of results")

	queryEventsCmd.Flags().String("source", "", "Filter by source")
	queryEventsCmd.Flags().String("since", "", "Time window (e.g., 24h, 7d)")
	queryEventsCmd.Flags().Int("limit", 50, "Maximum number of results")
}
