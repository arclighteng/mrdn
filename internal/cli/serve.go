package cli

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/arclighteng/mrdn/internal/api"
	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the API server",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		ctx := context.Background()
		pool, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer pool.Close()

		// Verify DB connectivity but don't auto-migrate.
		// Run `mrdn migrate` separately before starting the server.

		store := db.NewStore(pool)
		srv := api.NewServer(store)

		addr := fmt.Sprintf(":%d", cfg.Port)
		log.Printf("MRDN API server listening on %s", addr)
		return http.ListenAndServe(addr, srv.Handler())
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}
