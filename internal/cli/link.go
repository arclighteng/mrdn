package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/spf13/cobra"
)

var linkCmd = &cobra.Command{
	Use:   "link",
	Short: "Manually link an alias to a company entity",
	Long: `Insert a manual entity alias mapping.

Examples:
  mrdn link --alias "NVDA Corp" --entity NVDA
  mrdn link --alias "Nvidia Corporation" --entity NVDA --confidence 0.95`,
	RunE: func(cmd *cobra.Command, args []string) error {
		alias, _ := cmd.Flags().GetString("alias")
		entity, _ := cmd.Flags().GetString("entity")
		confidence, _ := cmd.Flags().GetFloat64("confidence")

		if alias == "" || entity == "" {
			return fmt.Errorf("both --alias and --entity are required")
		}

		entity = strings.ToUpper(entity)

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

		// Look up the company by ticker to get the entity ID.
		company, err := store.GetCompanyByTicker(ctx, entity)
		if err != nil {
			return fmt.Errorf("looking up company %s: %w", entity, err)
		}

		source := "manual"
		conf := confidence
		result, err := store.InsertEntityAlias(ctx, db.EntityAlias{
			EntityID:   company.ID,
			EntityType: "company",
			Alias:      alias,
			Source:     &source,
			Confidence: &conf,
		})
		if err != nil {
			return fmt.Errorf("inserting entity alias: %w", err)
		}

		if result.ID == 0 {
			fmt.Fprintf(os.Stdout, "Alias %q already linked (no change)\n", alias)
			return nil
		}
		fmt.Fprintf(os.Stdout, "Linked alias %q to %s (%s) with confidence %.2f\n",
			alias, company.Name, company.Ticker, confidence)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(linkCmd)

	linkCmd.Flags().String("alias", "", "The alias string to register")
	linkCmd.Flags().String("entity", "", "The company ticker to link to")
	linkCmd.Flags().Float64("confidence", 1.0, "Confidence score (0.0 to 1.0)")
}
