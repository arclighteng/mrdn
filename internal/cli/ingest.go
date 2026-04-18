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
	"github.com/arclighteng/mrdn/internal/resolver"
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

		d, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer d.Close()

		store := db.NewStore(d)
		b := broker.New(cfg.SSEMaxGlobal)
		defer b.Close()

		res, err := resolver.New(ctx, store)
		if err != nil {
			return fmt.Errorf("initializing resolver: %w", err)
		}

		sup := ingestion.NewSupervisor(cfg, store, b, ingestion.RealClock())
		sup.SetResolver(res)
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
