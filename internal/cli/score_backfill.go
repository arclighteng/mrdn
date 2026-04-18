package cli

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/arclighteng/mrdn/internal/score"
	"github.com/spf13/cobra"
)

var scoreBackfillWorkers int

var scoreBackfillCmd = &cobra.Command{
	Use:   "score-backfill",
	Short: "Compute scores for every company that has events",
	Long: `Synchronously runs the score engine for every company with at least one
linked event, bypassing the live ingestion budget. Use this to populate the
dashboard with real spread when starting fresh or after a long downtime.`,
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
		var runErr error
		var okCount, failCount int64
		defer func() {
			errStr := ""
			if runErr != nil {
				errStr = runErr.Error()
			}
			_ = store.RecordIngestAttempt(context.Background(), db.IngestAttempt{
				Source:     "score_engine",
				Success:    runErr == nil,
				Error:      errStr,
				Records:    int(atomic.LoadInt64(&okCount)),
				DurationMs: int(time.Since(startTime).Milliseconds()),
				HasNewData: atomic.LoadInt64(&okCount) > 0,
			})
			_ = failCount
		}()

		engine := score.NewEngine(
			store,
			score.NewMarketScorer(store),
			score.NewPolicyScorer(store),
			score.NewInsiderScorer(store),
			score.DefaultWeights(),
		)

		rows, err := d.QueryContext(ctx, `SELECT DISTINCT company_id FROM events WHERE company_id IS NOT NULL`)
		if err != nil {
			runErr = fmt.Errorf("querying companies with events: %w", err)
			return runErr
		}
		defer rows.Close()

		var ids []int
		for rows.Next() {
			var id int
			if err := rows.Scan(&id); err != nil {
				runErr = fmt.Errorf("scanning company id: %w", err)
				return runErr
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			runErr = fmt.Errorf("iterating company ids: %w", err)
			return runErr
		}

		workers := scoreBackfillWorkers
		if workers <= 0 {
			workers = 16
		}
		log.Printf("score-backfill: computing scores for %d companies with %d workers", len(ids), workers)
		now := time.Now()
		start := time.Now()

		jobs := make(chan int, workers*2)
		var wg sync.WaitGroup
		var done int64
		total := int64(len(ids))

		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for id := range jobs {
					if err := engine.ComputeAndStore(ctx, id, now); err != nil {
						atomic.AddInt64(&failCount, 1)
					} else {
						atomic.AddInt64(&okCount, 1)
					}
					n := atomic.AddInt64(&done, 1)
					if n%500 == 0 {
						log.Printf("score-backfill: progress %d/%d (%.0f%%)", n, total, float64(n)/float64(total)*100)
					}
				}
			}()
		}

		for _, id := range ids {
			select {
			case <-ctx.Done():
				close(jobs)
				wg.Wait()
				runErr = ctx.Err()
				return runErr
			case jobs <- id:
			}
		}
		close(jobs)
		wg.Wait()

		log.Printf("score-backfill: done in %s — %d ok, %d failed", time.Since(start).Round(time.Second), okCount, failCount)
		return nil
	},
}

func init() {
	scoreBackfillCmd.Flags().IntVar(&scoreBackfillWorkers, "workers", 16, "number of concurrent workers")
	rootCmd.AddCommand(scoreBackfillCmd)
}
