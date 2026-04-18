package cli

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/spf13/cobra"
)

var pruneKeepDays int

var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Delete old data to keep storage under budget",
	Long: `Removes events, market_data, and old scores beyond the retention window.
Keeps the most recent score per company regardless of age.`,
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

		cutoff := time.Now().AddDate(0, 0, -pruneKeepDays)
		log.Printf("[prune] deleting data older than %s (%d days)", cutoff.Format("2006-01-02"), pruneKeepDays)

		// Delete old market_data (biggest table after Finnhub removal).
		res, err := d.ExecContext(ctx, "DELETE FROM market_data WHERE recorded_at < ?", cutoff)
		if err != nil {
			return fmt.Errorf("pruning market_data: %w", err)
		}
		n, _ := res.RowsAffected()
		log.Printf("[prune] market_data: deleted %d rows", n)

		// Delete old scores but keep the latest per company.
		res, err = d.ExecContext(ctx, `
			DELETE FROM scores WHERE id IN (
				SELECT s.id FROM scores s
				WHERE s.computed_at < ?
				AND s.id NOT IN (
					SELECT id FROM scores
					GROUP BY company_id
					HAVING id = MAX(id)
				)
			)`, cutoff)
		if err != nil {
			return fmt.Errorf("pruning scores: %w", err)
		}
		n, _ = res.RowsAffected()
		log.Printf("[prune] scores: deleted %d rows", n)

		// Delete old events. Must handle FK references first.
		// Typed tables reference events via event_id.
		// NOTE: Raw ExecContext used here because Store has no bulk-delete methods.
		// Table names are compile-time constants, not user input — no injection risk.
		typedTables := []string{
			"congressional_trades", "contracts", "sanctions", "insider_trades",
			"donations", "lobbying", "court_filings", "warn_filings",
		}
		for _, table := range typedTables {
			res, err = d.ExecContext(ctx, fmt.Sprintf(
				"DELETE FROM %s WHERE event_id IN (SELECT id FROM events WHERE occurred_at < ?)", table), cutoff)
			if err != nil {
				return fmt.Errorf("pruning %s: %w", table, err)
			}
			n, _ = res.RowsAffected()
			if n > 0 {
				log.Printf("[prune] %s: deleted %d rows", table, n)
			}
		}

		res, err = d.ExecContext(ctx, "DELETE FROM events WHERE occurred_at < ?", cutoff)
		if err != nil {
			return fmt.Errorf("pruning events: %w", err)
		}
		n, _ = res.RowsAffected()
		log.Printf("[prune] events: deleted %d rows", n)

		log.Println("[prune] done")
		return nil
	},
}

func init() {
	pruneCmd.Flags().IntVar(&pruneKeepDays, "keep-days", 90, "number of days of data to retain")
	rootCmd.AddCommand(pruneCmd)
}
