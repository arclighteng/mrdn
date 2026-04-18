package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/spf13/cobra"
)

// enrich-companies fetches company names from Polygon's reference tickers API
// and updates any company row where name == ticker (i.e., name was never set).
// Also backfills sector from the Polygon SIC code when missing.

var enrichCompaniesCmd = &cobra.Command{
	Use:   "enrich-companies",
	Short: "Backfill company names and sectors from Polygon reference data",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		if cfg.PolygonAPIKey == "" {
			return fmt.Errorf("MRDN_POLYGON_API_KEY is required")
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		d, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer d.Close()

		// Find companies where name == ticker (placeholder names)
		rows, err := d.QueryContext(ctx, `SELECT ticker FROM companies WHERE name = ticker`)
		if err != nil {
			return fmt.Errorf("querying companies: %w", err)
		}
		var tickers []string
		for rows.Next() {
			var t string
			if err := rows.Scan(&t); err != nil {
				rows.Close()
				return err
			}
			tickers = append(tickers, t)
		}
		rows.Close()

		if len(tickers) == 0 {
			log.Println("enrich-companies: all companies already have names")
			return nil
		}
		log.Printf("enrich-companies: %d companies need names", len(tickers))

		// Fetch all US tickers from Polygon reference API (paginated)
		client := &http.Client{Timeout: 30 * time.Second}
		tickerMap := map[string]polygonTicker{}

		nextURL := fmt.Sprintf("https://api.polygon.io/v3/reference/tickers?market=stocks&active=true&limit=1000&apiKey=%s", cfg.PolygonAPIKey)
		for nextURL != "" {
			if ctx.Err() != nil {
				break
			}
			page, next, ferr := fetchPolygonTickers(ctx, client, nextURL)
			if ferr != nil {
				log.Printf("enrich-companies: fetch error: %v (continuing with what we have)", ferr)
				break
			}
			for _, t := range page {
				tickerMap[t.Ticker] = t
			}
			log.Printf("enrich-companies: fetched %d tickers so far", len(tickerMap))
			nextURL = next
			// Rate limit: Polygon free tier allows 5 req/min
			time.Sleep(250 * time.Millisecond)
		}

		log.Printf("enrich-companies: %d reference tickers loaded", len(tickerMap))

		var updated, notFound int
		for _, tk := range tickers {
			ref, ok := tickerMap[tk]
			if !ok {
				notFound++
				continue
			}
			// Only update the name — don't clobber existing sector/subsector
			_, err := d.ExecContext(ctx, `UPDATE companies SET name = ? WHERE ticker = ?`, ref.Name, tk)
			if err != nil {
				log.Printf("enrich-companies: update %s: %v", tk, err)
				continue
			}
			updated++
		}

		log.Printf("enrich-companies: done — %d updated, %d not found in Polygon", updated, notFound)
		return nil
	},
}

type polygonTicker struct {
	Ticker string `json:"ticker"`
	Name   string `json:"name"`
}

type polygonTickerResponse struct {
	Results []polygonTicker `json:"results"`
	NextURL string         `json:"next_url"`
}

func fetchPolygonTickers(ctx context.Context, client *http.Client, url string) ([]polygonTicker, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, "", fmt.Errorf("polygon tickers API: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, "", err
	}

	var pr polygonTickerResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		return nil, "", err
	}

	// next_url doesn't include apiKey — extract from current URL and append
	next := pr.NextURL
	if next != "" {
		// Polygon next_url already includes the apiKey in newer API versions,
		// but if not, we need to add it. Check if apiKey is present.
		if len(next) > 0 && !contains(next, "apiKey=") {
			sep := "?"
			if contains(next, "?") {
				sep = "&"
			}
			// Extract apiKey from original URL
			if idx := indexOf(url, "apiKey="); idx >= 0 {
				key := url[idx:]
				if amp := indexOf(key, "&"); amp >= 0 {
					key = key[:amp]
				}
				next = next + sep + key
			}
		}
	}

	return pr.Results, next, nil
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && indexOf(s, substr) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func init() {
	rootCmd.AddCommand(enrichCompaniesCmd)
}
