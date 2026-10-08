package config

import (
	"strings"
	"time"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/config"
)

const (
	defaultHTTPAddr        = ":8080"
	defaultLogLevel        = "info"
	defaultOTLPEndpoint    = "localhost:4317"
	defaultShutdownTimeout = 10 * time.Second

	defaultAuthGRPCAddr     = "localhost:50051"
	defaultCustomerGRPCAddr = "localhost:50052"

	defaultRedisAddr = "localhost:6379"

	defaultUserRefill = 100 * time.Millisecond
	defaultUserBurst  = 20
	defaultIPRefill   = 10 * time.Second
	defaultIPBurst    = 5

	devIssuer   = "mini-fintech-auth"
	devAudience = "mini-fintech"
	devAuthHost = "127.0.0.1:8081"
	devJWKSPath = "/.well-known/jwks.json"
)

type Config struct {
	HTTPAddr        string        `field:"HTTP_ADDR,optional"`
	LogLevel        string        `field:"LOG_LEVEL,optional"`
	OTLPEndpoint    string        `field:"OTEL_EXPORTER_OTLP_ENDPOINT,optional"`
	ShutdownTimeout time.Duration `field:"SHUTDOWN_TIMEOUT,optional"`

	AuthJWKSURL  string `field:"AUTH_JWKS_URL,optional"`
	AuthIssuer   string `field:"AUTH_ISSUER,optional"`
	AuthAudience string `field:"AUTH_AUDIENCE,optional"`
	AuthDev      bool   `field:"AUTH_DEV,optional"`

	AuthGRPCAddr     string `field:"AUTH_GRPC_ADDR,optional"`
	CustomerGRPCAddr string `field:"CUSTOMER_GRPC_ADDR,optional"`

	RedisAddr     string `field:"REDIS_ADDR,optional"`
	RedisPassword string `field:"REDIS_PASSWORD,optional"`
	RedisDB       int    `field:"REDIS_DB,optional"`

	RateLimitUserRefill time.Duration `field:"RATE_LIMIT_USER_REFILL,optional"`
	RateLimitUserBurst  int           `field:"RATE_LIMIT_USER_BURST,optional"`
	RateLimitIPRefill   time.Duration `field:"RATE_LIMIT_IP_REFILL,optional"`
	RateLimitIPBurst    int           `field:"RATE_LIMIT_IP_BURST,optional"`

	CORSAllowedOrigins string `field:"CORS_ALLOWED_ORIGINS,optional"`
}

func Load(getenv func(string) string) (Config, error) {
	cfg, err := config.Load[Config](getenv)
	if err != nil {
		return Config{}, err
	}

	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = defaultHTTPAddr
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = defaultLogLevel
	}
	if cfg.OTLPEndpoint == "" {
		cfg.OTLPEndpoint = defaultOTLPEndpoint
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = defaultShutdownTimeout
	}
	if cfg.AuthGRPCAddr == "" {
		cfg.AuthGRPCAddr = defaultAuthGRPCAddr
	}
	if cfg.CustomerGRPCAddr == "" {
		cfg.CustomerGRPCAddr = defaultCustomerGRPCAddr
	}
	if cfg.RedisAddr == "" {
		cfg.RedisAddr = defaultRedisAddr
	}
	if cfg.RateLimitUserRefill <= 0 {
		cfg.RateLimitUserRefill = defaultUserRefill
	}
	if cfg.RateLimitUserBurst <= 0 {
		cfg.RateLimitUserBurst = defaultUserBurst
	}
	if cfg.RateLimitIPRefill <= 0 {
		cfg.RateLimitIPRefill = defaultIPRefill
	}
	if cfg.RateLimitIPBurst <= 0 {
		cfg.RateLimitIPBurst = defaultIPBurst
	}
	if cfg.AuthDev {
		if cfg.AuthIssuer == "" {
			cfg.AuthIssuer = devIssuer
		}
		if cfg.AuthAudience == "" {
			cfg.AuthAudience = devAudience
		}
		if cfg.AuthJWKSURL == "" {
			cfg.AuthJWKSURL = devJWKSURL()
		}
	}

	return cfg, nil
}

func (c Config) UserRefillRate() float64 { return refillRate(c.RateLimitUserRefill) }

func (c Config) IPRefillRate() float64 { return refillRate(c.RateLimitIPRefill) }

func (c Config) CORSOrigins() []string { return splitList(c.CORSAllowedOrigins) }

func refillRate(interval time.Duration) float64 { return 1 / interval.Seconds() }

func devJWKSURL() string { return "http://" + devAuthHost + devJWKSPath }

func splitList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
