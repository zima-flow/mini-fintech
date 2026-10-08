package gatewayhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/interceptors"
	redisadapter "github.com/zima-flow/go-mentor/mini-fintech/services/api-gateway/internal/adapters/redis"
)

type fakeLimiter struct {
	decision redisadapter.Decision
	err      error
	keys     []string
}

func (f *fakeLimiter) Allow(_ context.Context, key string, _ redisadapter.Policy) (redisadapter.Decision, error) {
	f.keys = append(f.keys, key)
	return f.decision, f.err
}

func allowKey(*http.Request) (string, bool) { return "rl:test", true }

func TestRateLimit_Allows(t *testing.T) {
	t.Parallel()

	limiter := &fakeLimiter{decision: redisadapter.Decision{Allowed: true}}
	reached := false
	handler := newTestRouter(RateLimit(limiter, redisadapter.Policy{Name: "user"}, allowKey)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			reached = true
			w.WriteHeader(http.StatusNoContent)
		})))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/customer/profile", nil))

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.True(t, reached)
	require.Equal(t, []string{"rl:test"}, limiter.keys)
}

func TestRateLimit_DeniesWith429Envelope(t *testing.T) {
	t.Parallel()

	limiter := &fakeLimiter{decision: redisadapter.Decision{Allowed: false, RetryAfter: 7 * time.Second}}
	reached := false
	handler := newTestRouter(RateLimit(limiter, redisadapter.Policy{Name: "user"}, allowKey)(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/customer/profile", nil)
	req.Header.Set("X-Request-Id", "rid-9")
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Equal(t, "7", rec.Header().Get("Retry-After"))
	require.False(t, reached)

	var body ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "RESOURCE_EXHAUSTED", body.Code)
	require.Equal(t, "rid-9", body.RequestID)
}

func TestRateLimit_RetryAfterAtLeastOneSecond(t *testing.T) {
	t.Parallel()

	limiter := &fakeLimiter{decision: redisadapter.Decision{Allowed: false}}
	handler := newTestRouter(RateLimit(limiter, redisadapter.Policy{Name: "user"}, allowKey)(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/customer/profile", nil))

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Equal(t, "1", rec.Header().Get("Retry-After"))
}

func TestRateLimit_FailsOpenOnLimiterError(t *testing.T) {
	t.Parallel()

	limiter := &fakeLimiter{decision: redisadapter.Decision{Allowed: false}, err: context.Canceled}
	reached := false
	handler := newTestRouter(RateLimit(limiter, redisadapter.Policy{Name: "user"}, allowKey)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			reached = true
			w.WriteHeader(http.StatusNoContent)
		})))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/customer/profile", nil))

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.True(t, reached)
}

func TestRateLimit_NoKeySkipsLimiter(t *testing.T) {
	t.Parallel()

	limiter := &fakeLimiter{decision: redisadapter.Decision{Allowed: false}}
	reached := false
	handler := newTestRouter(RateLimit(limiter, redisadapter.Policy{Name: "user"},
		func(*http.Request) (string, bool) { return "", false })(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			reached = true
			w.WriteHeader(http.StatusNoContent)
		})))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/customer/profile", nil))

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.True(t, reached)
	require.Empty(t, limiter.keys)
}

func TestUserKey(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/v1/customer/profile", nil)
	_, ok := UserKey(req)
	require.False(t, ok, "no principal: no key")

	req = req.WithContext(interceptors.WithPrincipal(req.Context(), interceptors.Principal{Subject: "alice"}))
	key, ok := UserKey(req)
	require.True(t, ok)
	require.Equal(t, "rl:user:alice", key)
}

func TestIPKey(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	req.RemoteAddr = "203.0.113.7:5555"

	key, ok := IPKey(req)
	require.True(t, ok)
	require.Equal(t, "rl:ip:203.0.113.7:/v1/auth/login", key)
}
