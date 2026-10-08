package gatewayhttp

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

type CORSConfig struct {
	AllowedOrigins []string
	AllowedMethods []string
	AllowedHeaders []string
	MaxAge         time.Duration
}

func CORS(cfg CORSConfig) func(http.Handler) http.Handler {
	methods := strings.Join(orDefault(cfg.AllowedMethods,
		[]string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodOptions}), ", ")
	headers := strings.Join(orDefault(cfg.AllowedHeaders,
		[]string{"Authorization", "Content-Type", "Idempotency-Key", "X-Request-Id"}), ", ")

	var maxAge string
	if cfg.MaxAge > 0 {
		maxAge = strconv.Itoa(int(cfg.MaxAge.Seconds()))
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			allow, ok := allowedOrigin(cfg.AllowedOrigins, origin)
			if ok {
				w.Header().Set("Access-Control-Allow-Origin", allow)
				if allow != "*" {
					w.Header().Add("Vary", "Origin")
				}
			}

			if isPreflight(r) {
				if ok {
					w.Header().Set("Access-Control-Allow-Methods", methods)
					w.Header().Set("Access-Control-Allow-Headers", headers)
					if maxAge != "" {
						w.Header().Set("Access-Control-Max-Age", maxAge)
					}
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func isPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
}

func allowedOrigin(origins []string, origin string) (string, bool) {
	if len(origins) == 0 {
		return "*", true
	}
	for _, allowed := range origins {
		if allowed == "*" {
			return "*", true
		}
		if strings.EqualFold(allowed, origin) {
			return origin, true
		}
	}
	return "", false
}

func orDefault(values, fallback []string) []string {
	if len(values) == 0 {
		return fallback
	}
	return values
}
