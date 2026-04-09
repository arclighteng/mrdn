package cli

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/spf13/cobra"
)

var sourcesVerbose bool

var sourcesCmd = &cobra.Command{
	Use:   "sources",
	Short: "Show data source health and freshness",
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

		store := db.NewStore(pool)
		sources, err := store.ListSourceMeta(ctx)
		if err != nil {
			return fmt.Errorf("listing sources: %w", err)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "SOURCE\tSTATUS\tEXPECTED LAG\tLAST POLL\tLAST DATA")
		for _, s := range sources {
			lastPoll := "never"
			if s.LastSuccessfulPoll != nil {
				lastPoll = time.Since(*s.LastSuccessfulPoll).Truncate(time.Second).String() + " ago"
			}
			lastData := "never"
			if s.LastNewDataAt != nil {
				lastData = time.Since(*s.LastNewDataAt).Truncate(time.Second).String() + " ago"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				s.SourceName, s.Status, s.ExpectedLag, lastPoll, lastData)
		}
		w.Flush()

		if sourcesVerbose {
			fmt.Fprintln(os.Stdout)
			vw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(vw, "SOURCE\tLAST ATTEMPT\tHTTP\tLAST ERROR")
			for _, s := range sources {
				lastAttempt := "never"
				if s.LastAttemptAt != nil {
					lastAttempt = time.Since(*s.LastAttemptAt).Truncate(time.Second).String() + " ago"
				}
				httpCode := "-"
				if s.LastHTTPCode != nil {
					httpCode = fmt.Sprintf("%d", *s.LastHTTPCode)
				}
				lastError := "-"
				if s.LastError != nil && *s.LastError != "" {
					lastError = *s.LastError
				}
				fmt.Fprintf(vw, "%s\t%s\t%s\t%s\n",
					s.SourceName, lastAttempt, httpCode, lastError)
			}
			vw.Flush()
		}
		return nil
	},
}

func init() {
	sourcesCmd.Flags().BoolVarP(&sourcesVerbose, "verbose", "v", false,
		"show last attempt timestamp, HTTP code, and last error per source")
	rootCmd.AddCommand(sourcesCmd)
}
