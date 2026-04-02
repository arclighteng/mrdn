package config_test

import (
	"os"
	"testing"

	"github.com/arclighteng/mrdn/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_RequiresDatabaseURL(t *testing.T) {
	os.Unsetenv("DATABASE_URL")
	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_URL")
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://localhost/mrdn")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "postgresql://localhost/mrdn", cfg.DatabaseURL)
	assert.Equal(t, 8080, cfg.Port)
	assert.Equal(t, "info", cfg.LogLevel)
}

func TestLoad_OverridePort(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://localhost/mrdn")
	t.Setenv("MRDN_PORT", "9090")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, 9090, cfg.Port)
}
