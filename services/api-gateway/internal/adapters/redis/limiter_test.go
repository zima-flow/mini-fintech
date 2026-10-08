package redisadapter

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeBucket struct {
	tokens float64
	ts     time.Time
}

type fakeStore struct {
	mu       sync.Mutex
	buckets  map[string]*fakeBucket
	now      time.Time
	fixedErr error

	lastKey   string
	lastRate  float64
	lastBurst float64
	lastTTL   float64
}

func (f *fakeStore) Eval(_ context.Context, _ string, keys []string, args ...any) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(keys) > 0 {
		f.lastKey = keys[0]
	}
	if len(args) >= 3 {
		f.lastRate = toFloat(args[0])
		f.lastBurst = toFloat(args[1])
		f.lastTTL = toFloat(args[2])
	}
	if f.fixedErr != nil {
		return nil, f.fixedErr
	}

	now := f.now
	if now.IsZero() {
		now = time.Unix(0, 0)
	}
	key := keys[0]
	b := f.buckets[key]
	if b == nil {
		b = &fakeBucket{tokens: f.lastBurst, ts: now}
		if f.buckets == nil {
			f.buckets = make(map[string]*fakeBucket)
		}
		f.buckets[key] = b
	}
	if elapsed := now.Sub(b.ts).Seconds(); elapsed > 0 {
		b.tokens = math.Min(f.lastBurst, b.tokens+elapsed*f.lastRate)
		b.ts = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return []any{int64(1), int64(0)}, nil
	}
	return []any{int64(0), int64(math.Ceil((1 - b.tokens) / f.lastRate))}, nil
}

func (f *fakeStore) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}

func TestLimiter_AllowsWithinBurst(t *testing.T) {
	t.Parallel()

	store := &fakeStore{now: time.Unix(1000, 0)}
	limiter := NewLimiter(store, discardLogger(), nil)

	decision, err := limiter.Allow(context.Background(), "rl:user:u1", Policy{
		Name: "user", Rate: 10, Burst: 2, TTL: time.Minute,
	})

	require.NoError(t, err)
	require.True(t, decision.Allowed)
	require.Equal(t, "rl:user:u1", store.lastKey)
	require.Equal(t, 10.0, store.lastRate)
	require.Equal(t, 2.0, store.lastBurst)
	require.Equal(t, 60.0, store.lastTTL, "TTL is passed to Redis in whole seconds")
}

func TestLimiter_DeniesWhenExhausted(t *testing.T) {
	t.Parallel()

	store := &fakeStore{now: time.Unix(1000, 0)}
	limiter := NewLimiter(store, discardLogger(), nil)
	policy := Policy{Name: "user", Rate: 10, Burst: 1, TTL: time.Minute}

	first, err := limiter.Allow(context.Background(), "rl:user:u1", policy)
	require.NoError(t, err)
	require.True(t, first.Allowed)

	second, err := limiter.Allow(context.Background(), "rl:user:u1", policy)
	require.NoError(t, err)
	require.False(t, second.Allowed)
	require.Equal(t, time.Second, second.RetryAfter, "ceil(1 token / 10 per s) = 1s")
}

func TestLimiter_RefillsOverTime(t *testing.T) {
	t.Parallel()

	store := &fakeStore{now: time.Unix(1000, 0)}
	limiter := NewLimiter(store, discardLogger(), nil)
	policy := Policy{Name: "user", Rate: 1, Burst: 1, TTL: time.Minute}

	first, err := limiter.Allow(context.Background(), "rl:user:u1", policy)
	require.NoError(t, err)
	require.True(t, first.Allowed)

	second, err := limiter.Allow(context.Background(), "rl:user:u1", policy)
	require.NoError(t, err)
	require.False(t, second.Allowed)

	store.advance(time.Second)
	third, err := limiter.Allow(context.Background(), "rl:user:u1", policy)
	require.NoError(t, err)
	require.True(t, third.Allowed, "one token accrued after one second at 1/s")
}

func TestLimiter_FailsOpenOnRedisError(t *testing.T) {
	t.Parallel()

	store := &fakeStore{fixedErr: errors.New("dial tcp: connection refused")}
	limiter := NewLimiter(store, discardLogger(), nil)

	decision, err := limiter.Allow(context.Background(), "rl:user:u1", Policy{Rate: 10, Burst: 1})

	require.Error(t, err)
	require.True(t, decision.Allowed)
}

func TestLimiter_RecordsRedisErrorMetric(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	limiter := NewLimiter(&fakeStore{fixedErr: errors.New("down")}, discardLogger(), provider.Meter("test"))

	_, _ = limiter.Allow(context.Background(), "rl:user:u1", Policy{Name: "user", Rate: 10, Burst: 1})

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	require.Equal(t, int64(1), counterValue(t, rm, metricRedisErrors))
}

func TestLimiter_DisabledWhenNoRunner(t *testing.T) {
	t.Parallel()

	limiter := NewLimiter(nil, discardLogger(), nil)
	decision, err := limiter.Allow(context.Background(), "rl:user:u1", Policy{Rate: 1, Burst: 1})

	require.NoError(t, err)
	require.True(t, decision.Allowed)
}

func counterValue(t *testing.T, rm metricdata.ResourceMetrics, name string) int64 {
	t.Helper()

	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			require.True(t, ok, "metric %q is not an int64 sum", name)
			for _, dp := range sum.DataPoints {
				total += dp.Value
			}
		}
	}
	return total
}
