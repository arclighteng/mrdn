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

func TestLoad_SSEDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://localhost/mrdn")
	os.Unsetenv("MRDN_SSE_MAX_PER_IP")
	os.Unsetenv("MRDN_SSE_MAX_PER_KEY")
	os.Unsetenv("MRDN_SSE_MAX_GLOBAL")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, 3, cfg.SSEMaxPerIP)
	assert.Equal(t, 10, cfg.SSEMaxPerKey)
	assert.Equal(t, 500, cfg.SSEMaxGlobal)
}

func TestLoad_SSEOverrides(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://localhost/mrdn")
	t.Setenv("MRDN_SSE_MAX_PER_IP", "5")
	t.Setenv("MRDN_SSE_MAX_PER_KEY", "20")
	t.Setenv("MRDN_SSE_MAX_GLOBAL", "1000")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, 5, cfg.SSEMaxPerIP)
	assert.Equal(t, 20, cfg.SSEMaxPerKey)
	assert.Equal(t, 1000, cfg.SSEMaxGlobal)
}

func TestValidateIngestion_MissingKeys(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://localhost/mrdn")
	os.Unsetenv("MRDN_POLYGON_API_KEY")
	os.Unsetenv("MRDN_FEC_API_KEY")

	cfg, err := config.Load()
	require.NoError(t, err)

	err = cfg.ValidateIngestion()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MRDN_POLYGON_API_KEY")
	assert.Contains(t, err.Error(), "MRDN_FEC_API_KEY")
}

func TestValidateIngestion_AllPresent(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://localhost/mrdn")
	t.Setenv("MRDN_POLYGON_API_KEY", "poly-key")
	t.Setenv("MRDN_FEC_API_KEY", "fec-key")

	cfg, err := config.Load()
	require.NoError(t, err)

	err = cfg.ValidateIngestion()
	require.NoError(t, err)
}
