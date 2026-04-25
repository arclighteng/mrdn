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

// ingest-lambda-congress runs a single one-shot poll of the Lambda Finance
// congressional trades source and inserts whatever it returns. It does NOT
// require Finnhub/Polygon/FEC keys and does NOT run continuously — it polls
// once and exits.
var ingestLambdaCongressCmd = &cobra.Command{
	Use:   "ingest-lambda-congress",
	Short: "One-shot poll of the Lambda Finance congressional trades source",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		if cfg.LambdaAPIKey == "" {
			return fmt.Errorf("MRDN_LAMBDA_API_KEY environment variable is required")
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
		src := parser.NewLambdaCongressSource(client, cfg.LambdaAPIKey)

		log.Printf("ingest-lambda-congress: polling %s ...", src.Name())
		start := time.Now()

		var inserted, resolved, failed int
		var runErr error
		var httpCode int
		defer func() {
			errStr := ""
			if runErr != nil {
				errStr = runErr.Error()
			}
			_ = store.RecordIngestAttempt(context.Background(), db.IngestAttempt{
				Source:     src.Name(),
				Success:    runErr == nil,
				HTTPCode:   httpCode,
				Error:      errStr,
				Records:    inserted,
				DurationMs: int(time.Since(start).Milliseconds()),
				HasNewData: inserted > 0,
			})
		}()

		events, err := src.Poll(ctx)
		if err != nil {
			var code int
			_, _ = fmt.Sscanf(err.Error(), "http status %d", &code)
			httpCode = code
			runErr = fmt.Errorf("polling Lambda Finance: %w", err)
			return runErr
		}

		log.Printf("ingest-lambda-congress: %d events fetched in %s", len(events), time.Since(start).Round(time.Millisecond))

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

		if rerr := store.RecordPoll(ctx, src.Name(), len(events) > 0); rerr != nil {
			log.Printf("ingest-lambda-congress: warning — RecordPoll failed: %v", rerr)
		}

		log.Printf("ingest-lambda-congress: done — %d inserted, %d resolved to companies, %d failed", inserted, resolved, failed)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(ingestLambdaCongressCmd)
}
