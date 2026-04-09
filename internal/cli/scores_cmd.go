package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/spf13/cobra"
)

var scoresCmd = &cobra.Command{
	Use:   "scores",
	Short: "View risk scores — movers, rankings, or company detail",
	RunE: func(cmd *cobra.Command, args []string) error {
		movers, _ := cmd.Flags().GetBool("movers")
		rankings, _ := cmd.Flags().GetBool("rankings")
		company, _ := cmd.Flags().GetString("company")

		if !movers && !rankings && company == "" {
			return fmt.Errorf("specify one of --movers, --rankings, or --company TICKER")
		}

		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		ctx := context.Background()
		pool, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer pool.Close()

		store := db.NewStore(pool)

		switch {
		case movers:
			return runScoreMovers(ctx, cmd, store)
		case rankings:
			return runScoreRankings(ctx, cmd, store)
		default:
			return runScoreCompany(ctx, store, company)
		}
	},
}

func runScoreMovers(ctx context.Context, cmd *cobra.Command, store *db.Store) error {
	hours, _ := cmd.Flags().GetInt("hours")
	limit, _ := cmd.Flags().GetInt("limit")

	movers, err := store.GetScoreMovers(ctx, hours, limit)
	if err != nil {
		return fmt.Errorf("getting score movers: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TICKER\tCOMPANY\tPREVIOUS\tCURRENT\tCHANGE")
	fmt.Fprintln(w, "------\t-------\t--------\t-------\t------")
	for _, m := range movers {
		sign := "+"
		if m.Change < 0 {
			sign = ""
		}
		fmt.Fprintf(w, "%s\t%s\t%.1f\t%.1f\t%s%.1f\n",
			m.Ticker, m.CompanyName, m.PreviousScore, m.CurrentScore, sign, m.Change)
	}
	w.Flush()
	fmt.Fprintf(os.Stdout, "\n%d movers found\n", len(movers))
	return nil
}

func runScoreRankings(ctx context.Context, cmd *cobra.Command, store *db.Store) error {
	limit, _ := cmd.Flags().GetInt("limit")

	rankings, err := store.GetScoreRankings(ctx, limit)
	if err != nil {
		return fmt.Errorf("getting score rankings: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "RANK\tTICKER\tCOMPANY\tMARKET\tPOLICY\tINSIDER\tCOMPOSITE")
	fmt.Fprintln(w, "----\t------\t-------\t------\t------\t-------\t---------")
	for i, r := range rankings {
		fmt.Fprintf(w, "%d\t%s\t%s\t%.1f\t%.1f\t%.1f\t%.1f\n",
			i+1, r.Ticker, r.CompanyName,
			r.MarketScore, r.PolicyScore, r.InsiderScore, r.CompositeScore)
	}
	w.Flush()
	fmt.Fprintf(os.Stdout, "\n%d companies ranked\n", len(rankings))
	return nil
}

func runScoreCompany(ctx context.Context, store *db.Store, ticker string) error {
	ticker = strings.ToUpper(ticker)

	company, err := store.GetCompanyByTicker(ctx, ticker)
	if err != nil {
		return fmt.Errorf("looking up company %s: %w", ticker, err)
	}

	score, err := store.GetLatestScore(ctx, company.ID)
	if err != nil {
		return fmt.Errorf("getting score for %s: %w", ticker, err)
	}

	fmt.Fprintf(os.Stdout, "Company:    %s (%s)\n", company.Name, company.Ticker)
	fmt.Fprintf(os.Stdout, "Computed:   %s\n\n", score.ComputedAt.Format(time.RFC3339))

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "COMPONENT\tSCORE")
	fmt.Fprintln(w, "---------\t-----")
	fmt.Fprintf(w, "Market\t%.2f\n", score.MarketScore)
	fmt.Fprintf(w, "Policy\t%.2f\n", score.PolicyScore)
	fmt.Fprintf(w, "Insider\t%.2f\n", score.InsiderScore)
	fmt.Fprintf(w, "Composite\t%.2f\n", score.CompositeScore)
	w.Flush()

	fmt.Fprintf(os.Stdout, "\nWeight version: %d\n", score.WeightVersion)

	// Show recent score history.
	history, err := store.GetScoreHistory(ctx, company.ID, 5)
	if err != nil {
		return fmt.Errorf("getting score history for %s: %w", ticker, err)
	}

	if len(history) > 1 {
		fmt.Fprintf(os.Stdout, "\nRecent history:\n")
		hw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(hw, "COMPUTED_AT\tCOMPOSITE")
		fmt.Fprintln(hw, "-----------\t---------")
		for _, h := range history {
			fmt.Fprintf(hw, "%s\t%.2f\n", h.ComputedAt.Format(time.RFC3339), h.CompositeScore)
		}
		hw.Flush()
	}

	return nil
}

func init() {
	rootCmd.AddCommand(scoresCmd)

	scoresCmd.Flags().Bool("movers", false, "Show companies with biggest score changes")
	scoresCmd.Flags().Bool("rankings", false, "Show top companies by composite score")
	scoresCmd.Flags().String("company", "", "Show detailed score breakdown for a company ticker")
	scoresCmd.Flags().Int("hours", 24, "Time window in hours (used with --movers)")
	scoresCmd.Flags().Int("limit", 20, "Maximum number of results")
}
