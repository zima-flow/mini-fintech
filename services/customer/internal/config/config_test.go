package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/config"
)

func TestLoad_AppliesLocalDefaults(t *testing.T) {
	t.Parallel()

	env := map[string]string{"DATABASE_URL": "postgres://customer"}
	cfg, err := config.Load(func(key string) string { return env[key] })
	require.NoError(t, err)

	require.Equal(t, ":50052", cfg.GRPCAddr)
	require.Equal(t, "info", cfg.LogLevel)
	require.Equal(t, "localhost:4317", cfg.OTLPEndpoint)
	require.Equal(t, 10*time.Second, cfg.ShutdownTimeout)
	require.Equal(t, "localhost:9092", cfg.KafkaBrokers)
	require.Equal(t, "customer-service", cfg.KafkaGroup)
	require.Equal(t, []string{"localhost:9092"}, cfg.BrokerList())
}

func TestLoad_BrokerListSplitsAndTrims(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"DATABASE_URL":  "postgres://customer",
		"KAFKA_BROKERS": " kafka-1:9092, kafka-2:9092 , ",
	}
	cfg, err := config.Load(func(key string) string { return env[key] })
	require.NoError(t, err)
	require.Equal(t, []string{"kafka-1:9092", "kafka-2:9092"}, cfg.BrokerList())
}

func TestLoad_RequiresDatabaseURL(t *testing.T) {
	t.Parallel()

	_, err := config.Load(func(string) string { return "" })
	require.Error(t, err)
}

func TestLoad_DevDefaultsForIssuerAndJWKS(t *testing.T) {
	t.Parallel()

	env := map[string]string{"DATABASE_URL": "postgres://customer", "AUTH_DEV": "true"}
	cfg, err := config.Load(func(key string) string { return env[key] })
	require.NoError(t, err)

	require.Equal(t, "mini-fintech-auth", cfg.AuthIssuer)
	require.Equal(t, "mini-fintech", cfg.AuthAudience)
	require.Equal(t, "http://127.0.0.1:8081/.well-known/jwks.json", cfg.AuthJWKSURL)
}

func TestLoad_DevDefaults_RespectExplicitValues(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"DATABASE_URL":  "postgres://customer",
		"AUTH_DEV":      "true",
		"AUTH_ISSUER":   "https://issuer.example",
		"AUTH_AUDIENCE": "aud",
		"AUTH_JWKS_URL": "https://issuer.example/jwks.json",
	}
	cfg, err := config.Load(func(key string) string { return env[key] })
	require.NoError(t, err)

	require.Equal(t, "https://issuer.example", cfg.AuthIssuer)
	require.Equal(t, "aud", cfg.AuthAudience)
	require.Equal(t, "https://issuer.example/jwks.json", cfg.AuthJWKSURL)
}

func TestLoad_NonDevKeepsIdentityEmpty(t *testing.T) {
	t.Parallel()

	env := map[string]string{"DATABASE_URL": "postgres://customer"}
	cfg, err := config.Load(func(key string) string { return env[key] })
	require.NoError(t, err)

	require.Empty(t, cfg.AuthIssuer)
	require.Empty(t, cfg.AuthAudience)
	require.Empty(t, cfg.AuthJWKSURL)
}
