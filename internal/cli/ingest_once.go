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
	"github.com/arclighteng/mrdn/internal/ingestion"
	"github.com/arclighteng/mrdn/internal/resolver"
	"github.com/spf13/cobra"
)

var ingestOnceCmd = &cobra.Command{
	Use:   "ingest-once",
	Short: "Poll all sources once and exit",
	Long: `One-shot ingestion: polls every registered source exactly once,
resolves entities, and exits. Designed for cron-based ingestion in CI.
Does NOT run the score engine — run score-backfill separately.`,
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
		res, err := resolver.New(ctx, store)
		if err != nil {
			return fmt.Errorf("initializing resolver: %w", err)
		}

		// Reuse the supervisor's source registry to avoid source-list drift.
		sup := ingestion.NewSupervisor(cfg, store, nil, ingestion.RealClock())
		sources := sup.RegisterSources()

		var totalNew int
		for _, src := range sources {
			log.Printf("[ingest-once] polling %s...", src.Name())
			started := time.Now()
			events, err := src.Poll(ctx)
			dur := time.Since(started)
			if err != nil {
				log.Printf("[ingest-once] %s error (%s): %v", src.Name(), dur, err)
				_ = store.RecordIngestAttempt(ctx, db.IngestAttempt{
					Source:     src.Name(),
					Success:    false,
					Error:      err.Error(),
					DurationMs: int(dur.Milliseconds()),
				})
				continue
			}

			ids, berr := store.InsertEventsBatch(ctx, events)
			if berr != nil {
				log.Printf("[ingest-once] %s batch insert error: %v", src.Name(), berr)
			}

			newCount := 0
			for i, evt := range events {
				id := 0
				if i < len(ids) {
					id = ids[i]
				}
				if id == 0 {
					continue
				}
				newCount++
				evt.ID = id
				// Resolve() persists the company link internally via
				// store.UpdateEventCompanyID — no need to capture the return.
				res.Resolve(ctx, evt)
			}
			totalNew += newCount

			_ = store.RecordIngestAttempt(ctx, db.IngestAttempt{
				Source:     src.Name(),
				Success:    true,
				Records:    len(events),
				DurationMs: int(dur.Milliseconds()),
				HasNewData: newCount > 0,
			})
			log.Printf("[ingest-once] %s: %d events (%d new) in %s", src.Name(), len(events), newCount, dur)
		}

		log.Printf("[ingest-once] done — %d new events total", totalNew)

		// Backfill any events whose typed-table insertion failed on prior runs.
		backfilled, berr := res.Backfill(ctx, "")
		if berr != nil && ctx.Err() == nil {
			log.Printf("[ingest-once] backfill error: %v", berr)
		}
		if backfilled > 0 {
			log.Printf("[ingest-once] backfill resolved %d previously-orphaned events", backfilled)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(ingestOnceCmd)
}
