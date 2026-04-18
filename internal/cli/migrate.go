package cli

import (
	"context"
	"fmt"
	"log"

	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Run database migrations",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		ctx := context.Background()
		d, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer d.Close()

		if err := db.Migrate(ctx, d); err != nil {
			return fmt.Errorf("running migrations: %w", err)
		}

		log.Println("migrations complete")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(migrateCmd)
}
