package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all runtime configuration for the mrdn application.
type Config struct {
	DatabaseURL string
	Port        int
	LogLevel    string

	// External API keys — required only for ingestion.
	FinnhubAPIKey string
	PolygonAPIKey string
	FECAPIKey     string

	// SSE connection limits.
	SSEMaxPerIP  int
	SSEMaxPerKey int
	SSEMaxGlobal int
}

// Load reads configuration from environment variables. API keys are not
// validated here because they are only required when running mrdn ingest.
// Call ValidateIngestion to verify keys before starting an ingestion run.
func Load() (*Config, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	port := 8080
	if p := os.Getenv("MRDN_PORT"); p != "" {
		var err error
		port, err = strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("MRDN_PORT must be a number: %w", err)
		}
	}

	logLevel := "info"
	if l := os.Getenv("MRDN_LOG_LEVEL"); l != "" {
		logLevel = l
	}

	sseMaxPerIP, err := envInt("MRDN_SSE_MAX_PER_IP", 3)
	if err != nil {
		return nil, err
	}
	sseMaxPerKey, err := envInt("MRDN_SSE_MAX_PER_KEY", 10)
	if err != nil {
		return nil, err
	}
	sseMaxGlobal, err := envInt("MRDN_SSE_MAX_GLOBAL", 500)
	if err != nil {
		return nil, err
	}

	return &Config{
		DatabaseURL:   dbURL,
		Port:          port,
		LogLevel:      logLevel,
		FinnhubAPIKey: os.Getenv("MRDN_FINNHUB_API_KEY"),
		PolygonAPIKey: os.Getenv("MRDN_POLYGON_API_KEY"),
		FECAPIKey:     os.Getenv("MRDN_FEC_API_KEY"),
		SSEMaxPerIP:   sseMaxPerIP,
		SSEMaxPerKey:  sseMaxPerKey,
		SSEMaxGlobal:  sseMaxGlobal,
	}, nil
}

// ValidateIngestion returns an error if any API key required for ingestion is
// missing. Call this from the mrdn ingest command before starting work.
func (c *Config) ValidateIngestion() error {
	var missing []string
	if c.PolygonAPIKey == "" {
		missing = append(missing, "MRDN_POLYGON_API_KEY")
	}
	if c.FECAPIKey == "" {
		missing = append(missing, "MRDN_FEC_API_KEY")
	}
	if len(missing) > 0 {
		return fmt.Errorf("ingestion requires environment variables: %v", missing)
	}
	return nil
}

// envInt reads an integer from an environment variable, returning def if unset.
func envInt(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number: %w", key, err)
	}
	return n, nil
}
