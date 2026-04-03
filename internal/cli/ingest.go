package cli

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/arclighteng/mrdn/internal/broker"
	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/arclighteng/mrdn/internal/ingestion"
	"github.com/spf13/cobra"
)

var ingestCmd = &cobra.Command{
	Use:   "ingest",
	Short: "Start the ingestion workers",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		if err := cfg.ValidateIngestion(); err != nil {
			return fmt.Errorf("ingestion config invalid: %w", err)
		}

		// Trap SIGINT / SIGTERM for graceful shutdown.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		pool, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer pool.Close()

		store := db.NewStore(pool)
		b := broker.New(cfg.SSEMaxGlobal)
		defer b.Close()

		sup := ingestion.NewSupervisor(cfg, store, b, ingestion.RealClock())
		sup.Start()
		log.Println("ingestion supervisor started")

		<-ctx.Done()
		log.Println("shutting down ingestion supervisor...")

		sup.Stop()
		log.Println("ingestion supervisor stopped")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(ingestCmd)
}
