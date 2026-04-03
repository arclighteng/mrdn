package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/arclighteng/mrdn/internal/seeddata"
	"github.com/spf13/cobra"
)

type seedCompany struct {
	Ticker    string `json:"ticker"`
	Name      string `json:"name"`
	Sector    string `json:"sector"`
	Subsector string `json:"subsector"`
}

var seedCmd = &cobra.Command{
	Use:   "seed",
	Short: "Seed the database with initial company data",
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

		var companies []seedCompany
		if err := json.Unmarshal(seeddata.TechCompanies, &companies); err != nil {
			return fmt.Errorf("parsing seed data: %w", err)
		}

		for _, sc := range companies {
			_, err := store.UpsertCompany(ctx, db.Company{
				Ticker:    sc.Ticker,
				Name:      sc.Name,
				Sector:    db.StrPtr(sc.Sector),
				Subsector: db.StrPtr(sc.Subsector),
			})
			if err != nil {
				return fmt.Errorf("seeding %s: %w", sc.Ticker, err)
			}
		}

		log.Printf("seeded %d companies", len(companies))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(seedCmd)
}
