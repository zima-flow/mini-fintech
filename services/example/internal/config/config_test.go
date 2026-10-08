package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/services/example/internal/config"
)

func TestLoad_AppliesLocalDefaults(t *testing.T) {
	t.Parallel()

	env := map[string]string{"DATABASE_URL": "postgres://example"}
	cfg, err := config.Load(func(key string) string { return env[key] })
	require.NoError(t, err)

	require.Equal(t, ":50059", cfg.GRPCAddr)
	require.Equal(t, "info", cfg.LogLevel)
	require.Equal(t, "localhost:4317", cfg.OTLPEndpoint)
	require.Equal(t, 10*time.Second, cfg.ShutdownTimeout)
}

func TestLoad_RequiresDatabaseURL(t *testing.T) {
	t.Parallel()

	_, err := config.Load(func(string) string { return "" })
	require.Error(t, err)
}
