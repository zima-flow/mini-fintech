//go:build integration

package redisadapter_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	redisadapter "github.com/zima-flow/go-mentor/mini-fintech/services/api-gateway/internal/adapters/redis"
)

func startRedis(t *testing.T, ctx context.Context) redis.UniversalClient {
	t.Helper()

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "redis:7-alpine",
			ExposedPorts: []string{"6379/tcp"},
			WaitingFor:   wait.ForLog("Ready to accept connections").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	endpoint, err := ctr.PortEndpoint(ctx, "6379/tcp", "")
	require.NoError(t, err)

	client := redis.NewClient(&redis.Options{Addr: endpoint})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func newRealLimiter(client redis.UniversalClient) *redisadapter.Limiter {
	return redisadapter.NewLimiter(
		redisadapter.NewRedisRunner(client),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
	)
}

func TestLimiter_RealLua_AllowsBurstThenDeniesWithRetryAfter(t *testing.T) {
	ctx := context.Background()
	limiter := newRealLimiter(startRedis(t, ctx))
	policy := redisadapter.Policy{Name: "it", Rate: 10, Burst: 3, TTL: time.Minute}

	for i := 1; i <= 3; i++ {
		decision, err := limiter.Allow(ctx, "rl:user:it-burst", policy)
		require.NoError(t, err)
		require.Truef(t, decision.Allowed, "token %d is within the burst", i)
	}

	denied, err := limiter.Allow(ctx, "rl:user:it-burst", policy)
	require.NoError(t, err)
	require.False(t, denied.Allowed)
	require.Equal(t, time.Second, denied.RetryAfter, "ceil(1 token / 10 per s) = 1s")
}

func TestLimiter_RealLua_RefillsOverTime(t *testing.T) {
	ctx := context.Background()
	limiter := newRealLimiter(startRedis(t, ctx))
	policy := redisadapter.Policy{Name: "it", Rate: 1, Burst: 1, TTL: time.Minute}

	first, err := limiter.Allow(ctx, "rl:user:it-refill", policy)
	require.NoError(t, err)
	require.True(t, first.Allowed)

	second, err := limiter.Allow(ctx, "rl:user:it-refill", policy)
	require.NoError(t, err)
	require.False(t, second.Allowed)

	require.Eventually(t, func() bool {
		decision, err := limiter.Allow(ctx, "rl:user:it-refill", policy)
		return err == nil && decision.Allowed
	}, 5*time.Second, 100*time.Millisecond, "one token accrues after one second at 1/s")
}

func TestLimiter_RealLua_KeysAreIsolated(t *testing.T) {
	ctx := context.Background()
	limiter := newRealLimiter(startRedis(t, ctx))
	policy := redisadapter.Policy{Name: "it", Rate: 1, Burst: 1, TTL: time.Minute}

	a, err := limiter.Allow(ctx, "rl:user:it-a", policy)
	require.NoError(t, err)
	require.True(t, a.Allowed)

	exhausted, err := limiter.Allow(ctx, "rl:user:it-a", policy)
	require.NoError(t, err)
	require.False(t, exhausted.Allowed)

	b, err := limiter.Allow(ctx, "rl:user:it-b", policy)
	require.NoError(t, err)
	require.True(t, b.Allowed, "key b has its own bucket")
}

func TestLimiter_RealLua_SetsKeyTTL(t *testing.T) {
	ctx := context.Background()
	client := startRedis(t, ctx)
	limiter := newRealLimiter(client)
	policy := redisadapter.Policy{Name: "it", Rate: 10, Burst: 1, TTL: 90 * time.Second}

	_, err := limiter.Allow(ctx, "rl:user:it-ttl", policy)
	require.NoError(t, err)

	ttl, err := client.TTL(ctx, "rl:user:it-ttl").Result()
	require.NoError(t, err)
	require.Positive(t, ttl, "the bucket key has an expiry")
	require.LessOrEqual(t, ttl, 90*time.Second)
}
