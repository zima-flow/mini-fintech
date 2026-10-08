package gatewayhttp

import (
	"context"
	"math"
	"net"
	"net/http"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/interceptors"
	redisadapter "github.com/zima-flow/go-mentor/mini-fintech/services/api-gateway/internal/adapters/redis"
)

type RateLimiter interface {
	Allow(ctx context.Context, key string, policy redisadapter.Policy) (redisadapter.Decision, error)
}

type KeyFunc func(*http.Request) (string, bool)

func RateLimit(limiter RateLimiter, policy redisadapter.Policy, keyFn KeyFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if limiter == nil || keyFn == nil {
				next.ServeHTTP(w, r)
				return
			}
			key, ok := keyFn(r)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			decision, err := limiter.Allow(r.Context(), key, policy)
			if err != nil || decision.Allowed {
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(decision.RetryAfter)))
			writeError(w, r, status.Error(codes.ResourceExhausted, "rate limit exceeded"))
		})
	}
}

func UserKey(r *http.Request) (string, bool) {
	principal, ok := interceptors.PrincipalFromContext(r.Context())
	if !ok || principal.Subject == "" {
		return "", false
	}
	return "rl:user:" + principal.Subject, true
}

func IPKey(r *http.Request) (string, bool) {
	ip := clientIP(r)
	if ip == "" {
		return "", false
	}
	return "rl:ip:" + ip + ":" + r.URL.Path, true
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func retryAfterSeconds(d time.Duration) int {
	seconds := int(math.Ceil(d.Seconds()))
	if seconds < 1 {
		return 1
	}
	return seconds
}
