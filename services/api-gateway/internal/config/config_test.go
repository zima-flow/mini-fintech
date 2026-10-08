package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/services/api-gateway/internal/config"
)

func TestLoad_AppliesLocalDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(func(string) string { return "" })
	require.NoError(t, err)

	require.Equal(t, ":8080", cfg.HTTPAddr)
	require.Equal(t, "info", cfg.LogLevel)
	require.Equal(t, "localhost:4317", cfg.OTLPEndpoint)
	require.Equal(t, 10*time.Second, cfg.ShutdownTimeout)

	require.Equal(t, "localhost:50051", cfg.AuthGRPCAddr)
	require.Equal(t, "localhost:50052", cfg.CustomerGRPCAddr)
	require.Equal(t, "localhost:6379", cfg.RedisAddr)
	require.Zero(t, cfg.RedisDB)

	require.Equal(t, 100*time.Millisecond, cfg.RateLimitUserRefill)
	require.Equal(t, 20, cfg.RateLimitUserBurst)
	require.Equal(t, 10*time.Second, cfg.RateLimitIPRefill)
	require.Equal(t, 5, cfg.RateLimitIPBurst)
	require.InDelta(t, 10.0, cfg.UserRefillRate(), 1e-9)
	require.InDelta(t, 0.1, cfg.IPRefillRate(), 1e-9)

	require.Empty(t, cfg.CORSOrigins())
}

func TestLoad_OverridesWiringFromEnvironment(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"AUTH_GRPC_ADDR":         "auth.internal:50051",
		"CUSTOMER_GRPC_ADDR":     "customer.internal:50052",
		"REDIS_ADDR":             "redis.internal:6380",
		"REDIS_PASSWORD":         "secret",
		"REDIS_DB":               "3",
		"RATE_LIMIT_USER_REFILL": "50ms",
		"RATE_LIMIT_USER_BURST":  "40",
		"RATE_LIMIT_IP_REFILL":   "5s",
		"RATE_LIMIT_IP_BURST":    "10",
		"CORS_ALLOWED_ORIGINS":   "https://app.example, https://admin.example",
	}
	cfg, err := config.Load(func(key string) string { return env[key] })
	require.NoError(t, err)

	require.Equal(t, "auth.internal:50051", cfg.AuthGRPCAddr)
	require.Equal(t, "customer.internal:50052", cfg.CustomerGRPCAddr)
	require.Equal(t, "redis.internal:6380", cfg.RedisAddr)
	require.Equal(t, "secret", cfg.RedisPassword)
	require.Equal(t, 3, cfg.RedisDB)
	require.Equal(t, 50*time.Millisecond, cfg.RateLimitUserRefill)
	require.Equal(t, 40, cfg.RateLimitUserBurst)
	require.Equal(t, 5*time.Second, cfg.RateLimitIPRefill)
	require.Equal(t, 10, cfg.RateLimitIPBurst)
	require.InDelta(t, 20.0, cfg.UserRefillRate(), 1e-9)
	require.InDelta(t, 0.2, cfg.IPRefillRate(), 1e-9)
	require.Equal(t, []string{"https://app.example", "https://admin.example"}, cfg.CORSOrigins())
}

func TestLoad_DevDerivesJWKSURL(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(func(key string) string {
		if key == "AUTH_DEV" {
			return "true"
		}
		return ""
	})
	require.NoError(t, err)
	require.Equal(t, "mini-fintech-auth", cfg.AuthIssuer)
	require.Equal(t, "mini-fintech", cfg.AuthAudience)
	require.Equal(t, "http://127.0.0.1:8081/.well-known/jwks.json", cfg.AuthJWKSURL)

	explicit, err := config.Load(func(key string) string {
		switch key {
		case "AUTH_DEV":
			return "true"
		case "AUTH_JWKS_URL":
			return "https://auth.internal/.well-known/jwks.json"
		}
		return ""
	})
	require.NoError(t, err)
	require.Equal(t, "https://auth.internal/.well-known/jwks.json", explicit.AuthJWKSURL)
}

func TestLoad_OverridesFromEnvironment(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"HTTP_ADDR":        ":9090",
		"LOG_LEVEL":        "debug",
		"SHUTDOWN_TIMEOUT": "3s",
	}
	cfg, err := config.Load(func(key string) string { return env[key] })
	require.NoError(t, err)

	require.Equal(t, ":9090", cfg.HTTPAddr)
	require.Equal(t, "debug", cfg.LogLevel)
	require.Equal(t, 3*time.Second, cfg.ShutdownTimeout)
}
