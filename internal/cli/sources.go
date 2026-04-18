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

var sourcesCmd = &cobra.Command{
	Use:   "sources",
	Short: "Show data source health and freshness",
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

		store := db.NewStore(d)
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
		return nil
	},
}

func init() {
	rootCmd.AddCommand(sourcesCmd)
}
