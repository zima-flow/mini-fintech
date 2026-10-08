package redisadapter

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const tokenBucketScript = `
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local ttl = tonumber(ARGV[3])

local now = redis.call('TIME')
local secs = tonumber(now[1]) + tonumber(now[2]) / 1000000

local bucket = redis.call('HMGET', KEYS[1], 'tokens', 'ts')
local tokens = tonumber(bucket[1])
local ts = tonumber(bucket[2])
if tokens == nil then
  tokens = burst
  ts = secs
end

local elapsed = secs - ts
if elapsed > 0 then
  tokens = math.min(burst, tokens + elapsed * rate)
  ts = secs
end

local allowed = 0
local retry_after = 0
if tokens >= 1 then
  allowed = 1
  tokens = tokens - 1
else
  retry_after = math.ceil((1 - tokens) / rate)
end

redis.call('HSET', KEYS[1], 'tokens', tokens, 'ts', ts)
redis.call('EXPIRE', KEYS[1], ttl)
return {allowed, retry_after}
`

const metricRedisErrors = "gateway.ratelimit.redis_errors"

type Runner interface {
	Eval(ctx context.Context, script string, keys []string, args ...any) (any, error)
}

type RedisRunner struct {
	client redis.UniversalClient
}

func NewRedisRunner(client redis.UniversalClient) RedisRunner {
	return RedisRunner{client: client}
}

func (r RedisRunner) Eval(ctx context.Context, script string, keys []string, args ...any) (any, error) {
	return r.client.Eval(ctx, script, keys, args...).Result()
}

type Policy struct {
	Name  string
	Rate  float64
	Burst int
	TTL   time.Duration
}

type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
}

type Limiter struct {
	runner Runner
	log    *slog.Logger
	errors metric.Int64Counter
}

func NewLimiter(runner Runner, log *slog.Logger, meter metric.Meter) *Limiter {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	l := &Limiter{runner: runner, log: log}
	if meter != nil {
		counter, err := meter.Int64Counter(
			metricRedisErrors,
			metric.WithDescription("Redis failures in the gateway rate limiter (fail-open)"),
		)
		if err == nil {
			l.errors = counter
		} else {
			log.ErrorContext(context.Background(), "rate limiter: create metric", slog.Any("error", err))
		}
	}
	return l
}

func (l *Limiter) Allow(ctx context.Context, key string, policy Policy) (Decision, error) {
	if l == nil || l.runner == nil || key == "" {
		return Decision{Allowed: true}, nil
	}
	if policy.Rate <= 0 || policy.Burst < 1 {
		err := fmt.Errorf("redisadapter: invalid policy %q: rate=%v burst=%d", policy.Name, policy.Rate, policy.Burst)
		l.recordError(ctx, policy, err)
		return Decision{Allowed: true}, err
	}

	reply, err := l.runner.Eval(ctx, tokenBucketScript, []string{key}, policy.Rate, policy.Burst, ttlSeconds(policy))
	if err != nil {
		l.recordError(ctx, policy, err)
		return Decision{Allowed: true}, err
	}

	decision, err := parseReply(reply)
	if err != nil {
		l.recordError(ctx, policy, err)
		return Decision{Allowed: true}, err
	}
	return decision, nil
}

func (l *Limiter) recordError(ctx context.Context, policy Policy, err error) {
	if l.errors != nil {
		l.errors.Add(ctx, 1, metric.WithAttributes(attribute.String("policy", policy.Name)))
	}
	l.log.WarnContext(ctx, "rate limiter unavailable; failing open",
		slog.String("policy", policy.Name),
		slog.String("error", err.Error()),
	)
}

func ttlSeconds(policy Policy) int {
	if policy.TTL > 0 {
		return int(math.Ceil(policy.TTL.Seconds()))
	}
	return int(math.Ceil(float64(policy.Burst)/policy.Rate)) + 1
}

func parseReply(reply any) (Decision, error) {
	values, ok := reply.([]any)
	if !ok || len(values) != 2 {
		return Decision{}, fmt.Errorf("redisadapter: unexpected bucket reply %T", reply)
	}
	allowed, err := toInt64(values[0])
	if err != nil {
		return Decision{}, err
	}
	retryAfter, err := toInt64(values[1])
	if err != nil {
		return Decision{}, err
	}
	return Decision{Allowed: allowed == 1, RetryAfter: time.Duration(retryAfter) * time.Second}, nil
}

func toInt64(v any) (int64, error) {
	switch n := v.(type) {
	case int64:
		return n, nil
	case int:
		return int64(n), nil
	default:
		return 0, fmt.Errorf("redisadapter: non-integer bucket reply field %T", v)
	}
}
