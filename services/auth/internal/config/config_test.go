package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/config"
)

func TestLoad_AppliesLocalDefaults(t *testing.T) {
	t.Parallel()

	env := map[string]string{"DATABASE_URL": "postgres://example"}
	cfg, err := config.Load(func(key string) string { return env[key] })
	require.NoError(t, err)

	require.Equal(t, ":50051", cfg.GRPCAddr)
	require.Equal(t, ":8081", cfg.HTTPAddr)
	require.Equal(t, "info", cfg.LogLevel)
	require.Equal(t, "localhost:4317", cfg.OTLPEndpoint)
	require.Equal(t, 10*time.Second, cfg.ShutdownTimeout)
}

func TestLoad_RequiresDatabaseURL(t *testing.T) {
	t.Parallel()

	_, err := config.Load(func(string) string { return "" })
	require.Error(t, err)
}

func TestLoad_AppliesTokenDefaults(t *testing.T) {
	t.Parallel()

	env := map[string]string{"DATABASE_URL": "postgres://example"}
	cfg, err := config.Load(func(key string) string { return env[key] })
	require.NoError(t, err)

	require.Equal(t, 15*time.Minute, cfg.AuthAccessTTL)
	require.Equal(t, 30*24*time.Hour, cfg.AuthRefreshTTL)
}

func TestLoad_DevDefaultsForIssuerAndJWKS(t *testing.T) {
	t.Parallel()

	env := map[string]string{"DATABASE_URL": "postgres://example", "AUTH_DEV": "true"}
	cfg, err := config.Load(func(key string) string { return env[key] })
	require.NoError(t, err)

	require.Equal(t, "mini-fintech-auth", cfg.AuthIssuer)
	require.Equal(t, "mini-fintech", cfg.AuthAudience)
	require.Equal(t, "http://127.0.0.1:8081/.well-known/jwks.json", cfg.AuthJWKSURL)
}

func TestLoad_DevDefaults_RespectExplicitValues(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"DATABASE_URL":  "postgres://example",
		"AUTH_DEV":      "true",
		"AUTH_ISSUER":   "https://issuer.example",
		"AUTH_AUDIENCE": "aud",
		"AUTH_JWKS_URL": "https://issuer.example/jwks.json",
		"HTTP_ADDR":     "127.0.0.1:9999",
	}
	cfg, err := config.Load(func(key string) string { return env[key] })
	require.NoError(t, err)

	require.Equal(t, "https://issuer.example", cfg.AuthIssuer)
	require.Equal(t, "aud", cfg.AuthAudience)
	require.Equal(t, "https://issuer.example/jwks.json", cfg.AuthJWKSURL)
}

func TestLoad_NonDevKeepsIdentityEmpty(t *testing.T) {
	t.Parallel()

	env := map[string]string{"DATABASE_URL": "postgres://example"}
	cfg, err := config.Load(func(key string) string { return env[key] })
	require.NoError(t, err)

	require.Empty(t, cfg.AuthIssuer)
	require.Empty(t, cfg.AuthAudience)
}

func TestConfig_PublishedKeyPaths(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"DATABASE_URL":                 "postgres://example",
		"AUTH_JWT_PUBLISHED_KEY_PATHS": "/keys/old.pem, /keys/older.pem ",
	}
	cfg, err := config.Load(func(key string) string { return env[key] })
	require.NoError(t, err)

	require.Equal(t, []string{"/keys/old.pem", "/keys/older.pem"}, cfg.PublishedKeyPaths())
}

func TestConfig_PublishedKeyPaths_Empty(t *testing.T) {
	t.Parallel()

	env := map[string]string{"DATABASE_URL": "postgres://example"}
	cfg, err := config.Load(func(key string) string { return env[key] })
	require.NoError(t, err)

	require.Empty(t, cfg.PublishedKeyPaths())
}

func TestLoad_AppliesKafkaDefaults(t *testing.T) {
	t.Parallel()

	env := map[string]string{"DATABASE_URL": "postgres://example"}
	cfg, err := config.Load(func(key string) string { return env[key] })
	require.NoError(t, err)

	require.Equal(t, []string{"localhost:9092"}, cfg.BrokerList())
	require.Equal(t, "auth.customer-events", cfg.KafkaGroup)
}

func TestConfig_BrokerList(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"DATABASE_URL":  "postgres://example",
		"KAFKA_BROKERS": " kafka-1:9092 , kafka-2:9092 ",
	}
	cfg, err := config.Load(func(key string) string { return env[key] })
	require.NoError(t, err)

	require.Equal(t, []string{"kafka-1:9092", "kafka-2:9092"}, cfg.BrokerList())
}
