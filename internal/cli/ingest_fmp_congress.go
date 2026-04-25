package cli

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/arclighteng/mrdn/internal/parser"
	"github.com/arclighteng/mrdn/internal/resolver"
	"github.com/spf13/cobra"
)

// ingest-fmp-congress runs a single one-shot poll of the FMP senate-latest
// and house-latest endpoints, inserts events, and resolves them to persons
// and companies. Requires MRDN_FMP_API_KEY.
var ingestFMPCongressCmd = &cobra.Command{
	Use:   "ingest-fmp-congress",
	Short: "One-shot poll of FMP congressional trading endpoints (Senate + House)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		if cfg.FMPAPIKey == "" {
			return fmt.Errorf("MRDN_FMP_API_KEY is required for FMP congress ingestion")
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

		client := &http.Client{Timeout: 60 * time.Second}
		src := parser.NewFMPCongressSource(client, cfg.FMPAPIKey)

		log.Printf("ingest-fmp-congress: polling %s ...", src.Name())
		start := time.Now()

		var inserted, resolved, failed int
		var runErr error
		defer func() {
			errStr := ""
			if runErr != nil {
				errStr = runErr.Error()
			}
			_ = store.RecordIngestAttempt(context.Background(), db.IngestAttempt{
				Source:     src.Name(),
				Success:    runErr == nil,
				Error:      errStr,
				Records:    inserted,
				DurationMs: int(time.Since(start).Milliseconds()),
				HasNewData: inserted > 0,
			})
		}()

		events, err := src.Poll(ctx)
		if err != nil {
			runErr = fmt.Errorf("polling FMP congress: %w", err)
			return runErr
		}

		log.Printf("ingest-fmp-congress: %d events fetched in %s", len(events), time.Since(start).Round(time.Millisecond))

		for _, evt := range events {
			id, ierr := store.InsertEvent(ctx, evt)
			if ierr != nil {
				failed++
				continue
			}
			if id == 0 {
				continue // duplicate
			}
			inserted++
			evt.ID = id
			if cid := res.Resolve(ctx, evt); cid > 0 {
				resolved++
			}
		}

		log.Printf("ingest-fmp-congress: done — %d inserted, %d resolved, %d failed (of %d fetched)",
			inserted, resolved, failed, len(events))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(ingestFMPCongressCmd)
}
