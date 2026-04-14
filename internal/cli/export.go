package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/arclighteng/mrdn/internal/export"
	"github.com/spf13/cobra"
)

var exportOutDir string

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export all dashboard data as static JSON files",
	Long: `Reads from the database and writes pre-computed JSON files to the
output directory. These files are deployed to Cloudflare Pages to serve
the dashboard without a live API.`,
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
		return export.Run(ctx, store, exportOutDir)
	},
}

func init() {
	exportCmd.Flags().StringVar(&exportOutDir, "out", "dist/data", "output directory for JSON files")
	rootCmd.AddCommand(exportCmd)
}
