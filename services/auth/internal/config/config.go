package config

import (
	"errors"
	"strings"
	"time"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/config"
)

const (
	defaultGRPCAddr        = ":50051"
	defaultHTTPAddr        = ":8081"
	defaultLogLevel        = "info"
	defaultOTLPEndpoint    = "localhost:4317"
	defaultShutdownTimeout = 10 * time.Second

	defaultAccessTTL  = 15 * time.Minute
	defaultRefreshTTL = 30 * 24 * time.Hour

	defaultKafkaBrokers = "localhost:9092"
	defaultKafkaGroup   = "auth.customer-events"

	devIssuer   = "mini-fintech-auth"
	devAudience = "mini-fintech"
)

type Config struct {
	GRPCAddr        string        `field:"GRPC_ADDR,optional"`
	HTTPAddr        string        `field:"HTTP_ADDR,optional"`
	LogLevel        string        `field:"LOG_LEVEL,optional"`
	DatabaseURL     string        `field:"DATABASE_URL"`
	OTLPEndpoint    string        `field:"OTEL_EXPORTER_OTLP_ENDPOINT,optional"`
	ShutdownTimeout time.Duration `field:"SHUTDOWN_TIMEOUT,optional"`

	AuthJWKSURL  string `field:"AUTH_JWKS_URL,optional"`
	AuthIssuer   string `field:"AUTH_ISSUER,optional"`
	AuthAudience string `field:"AUTH_AUDIENCE,optional"`
	AuthDev      bool   `field:"AUTH_DEV,optional"`

	AuthJWTKeyPath       string        `field:"AUTH_JWT_KEY_PATH,optional"`
	AuthJWTKeyID         string        `field:"AUTH_JWT_KEY_ID,optional"`
	AuthJWTPublishedKeys string        `field:"AUTH_JWT_PUBLISHED_KEY_PATHS,optional"`
	AuthAccessTTL        time.Duration `field:"AUTH_ACCESS_TTL,optional"`
	AuthRefreshTTL       time.Duration `field:"AUTH_REFRESH_TTL,optional"`

	KafkaBrokers string `field:"KAFKA_BROKERS,optional"`
	KafkaGroup   string `field:"KAFKA_GROUP,optional"`
}

func Load(getenv func(string) string) (Config, error) {
	cfg, err := config.Load[Config](getenv)
	if err != nil {
		return Config{}, err
	}

	if cfg.GRPCAddr == "" {
		cfg.GRPCAddr = defaultGRPCAddr
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
	if cfg.AuthAccessTTL <= 0 {
		cfg.AuthAccessTTL = defaultAccessTTL
	}
	if cfg.AuthRefreshTTL <= 0 {
		cfg.AuthRefreshTTL = defaultRefreshTTL
	}
	if cfg.KafkaBrokers == "" {
		cfg.KafkaBrokers = defaultKafkaBrokers
	}
	if cfg.KafkaGroup == "" {
		cfg.KafkaGroup = defaultKafkaGroup
	}
	if cfg.AuthDev {
		if cfg.AuthIssuer == "" {
			cfg.AuthIssuer = devIssuer
		}
		if cfg.AuthAudience == "" {
			cfg.AuthAudience = devAudience
		}
		if cfg.AuthJWKSURL == "" {
			cfg.AuthJWKSURL = devJWKSURL(cfg.HTTPAddr)
		}
	}

	return cfg, nil
}

func (c Config) PublishedKeyPaths() []string {
	return splitList(c.AuthJWTPublishedKeys)
}

func (c Config) BrokerList() []string {
	return splitList(c.KafkaBrokers)
}

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

func devJWKSURL(httpAddr string) string {
	host := httpAddr
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	}
	return "http://" + host + "/.well-known/jwks.json"
}

func (c Config) Validate() error {
	if c.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	return nil
}
