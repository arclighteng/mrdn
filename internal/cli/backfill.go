package cli

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/arclighteng/mrdn/internal/resolver"
	"github.com/spf13/cobra"
)

var backfillCmd = &cobra.Command{
	Use:   "backfill [source]",
	Short: "Resolve unlinked events to companies and populate typed tables",
	Long: `Processes events with NULL company_id, matches them to companies,
and inserts typed records (market_data, insider_trades, etc.).
Optionally filter by source name (e.g., "polygon", "edgar_form4").`,
	Args: cobra.MaximumNArgs(1),
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

		store := db.NewStore(pool)

		res, err := resolver.New(ctx, store)
		if err != nil {
			return fmt.Errorf("initializing resolver: %w", err)
		}

		source := ""
		if len(args) > 0 {
			source = args[0]
		}

		log.Printf("starting backfill (source filter: %q)", source)
		resolved, err := res.Backfill(ctx, source)
		if err != nil {
			return fmt.Errorf("backfill: %w", err)
		}

		log.Printf("backfill complete: %d events resolved", resolved)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(backfillCmd)
}
