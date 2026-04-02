package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	DatabaseURL string
	Port        int
	LogLevel    string
}

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

	return &Config{
		DatabaseURL: dbURL,
		Port:        port,
		LogLevel:    logLevel,
	}, nil
}
