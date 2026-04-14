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

		pool, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer pool.Close()

		cutoff := time.Now().AddDate(0, 0, -pruneKeepDays)
		log.Printf("[prune] deleting data older than %s (%d days)", cutoff.Format("2006-01-02"), pruneKeepDays)

		// Delete old market_data (biggest table after Finnhub removal).
		tag, err := pool.Exec(ctx, "DELETE FROM market_data WHERE recorded_at < $1", cutoff)
		if err != nil {
			return fmt.Errorf("pruning market_data: %w", err)
		}
		log.Printf("[prune] market_data: deleted %d rows", tag.RowsAffected())

		// Delete old scores but keep the latest per company.
		tag, err = pool.Exec(ctx, `
			DELETE FROM scores WHERE id IN (
				SELECT s.id FROM scores s
				WHERE s.computed_at < $1
				AND s.id NOT IN (
					SELECT DISTINCT ON (company_id) id
					FROM scores ORDER BY company_id, computed_at DESC
				)
			)`, cutoff)
		if err != nil {
			return fmt.Errorf("pruning scores: %w", err)
		}
		log.Printf("[prune] scores: deleted %d rows", tag.RowsAffected())

		// Delete old events. Must handle FK references first.
		// Typed tables reference events via event_id.
		// NOTE: Raw pool.Exec used here because Store has no bulk-delete methods.
		// Table names are compile-time constants, not user input — no injection risk.
		typedTables := []string{
			"congressional_trades", "contracts", "sanctions", "insider_trades",
			"donations", "lobbying", "court_filings", "warn_filings",
		}
		for _, table := range typedTables {
			tag, err = pool.Exec(ctx, fmt.Sprintf(
				"DELETE FROM %s WHERE event_id IN (SELECT id FROM events WHERE occurred_at < $1)", table), cutoff)
			if err != nil {
				return fmt.Errorf("pruning %s: %w", table, err)
			}
			if tag.RowsAffected() > 0 {
				log.Printf("[prune] %s: deleted %d rows", table, tag.RowsAffected())
			}
		}

		tag, err = pool.Exec(ctx, "DELETE FROM events WHERE occurred_at < $1", cutoff)
		if err != nil {
			return fmt.Errorf("pruning events: %w", err)
		}
		log.Printf("[prune] events: deleted %d rows", tag.RowsAffected())

		log.Println("[prune] done")
		return nil
	},
}

func init() {
	pruneCmd.Flags().IntVar(&pruneKeepDays, "keep-days", 90, "number of days of data to retain")
	rootCmd.AddCommand(pruneCmd)
}
