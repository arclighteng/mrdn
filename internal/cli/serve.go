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

	"github.com/arclighteng/mrdn/internal/api"
	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/arclighteng/mrdn/web"
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

		// Trap SIGINT / SIGTERM for graceful shutdown.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		pool, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer pool.Close()

		// Verify DB connectivity but don't auto-migrate.
		// Run `mrdn migrate` separately before starting the server.

		store := db.NewStore(pool)
		srv := api.NewServer(store)
		srv.SetStaticFS(web.Static)

		addr := fmt.Sprintf(":%d", cfg.Port)
		httpServer := &http.Server{
			Addr:              addr,
			Handler:           srv.Handler(),
			ReadTimeout:       10 * time.Second,
			ReadHeaderTimeout: 5 * time.Second,
			WriteTimeout:      0, // Disabled: SSE connections are long-lived; lifecycle managed by serveSSE (30 min max).
			IdleTimeout:       120 * time.Second,
			MaxHeaderBytes:    1 << 20,
		}

		go func() {
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("listen: %v", err)
			}
		}()

		log.Printf("MRDN API server listening on %s", addr)
		<-ctx.Done()
		log.Println("shutting down...")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP server shutdown error: %v", err)
		}
		srv.Shutdown()
		log.Println("server stopped")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}
