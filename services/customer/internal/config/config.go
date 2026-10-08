package config

import (
	"errors"
	"strings"
	"time"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/config"
)

const (
	defaultGRPCAddr        = ":50052"
	defaultLogLevel        = "info"
	defaultOTLPEndpoint    = "localhost:4317"
	defaultShutdownTimeout = 10 * time.Second

	defaultKafkaBrokers = "localhost:9092"
	defaultKafkaGroup   = "customer-service"

	devIssuer   = "mini-fintech-auth"
	devAudience = "mini-fintech"
	devAuthHost = "127.0.0.1:8081"
	devJWKSPath = "/.well-known/jwks.json"
)

type Config struct {
	GRPCAddr        string        `field:"GRPC_ADDR,optional"`
	LogLevel        string        `field:"LOG_LEVEL,optional"`
	DatabaseURL     string        `field:"DATABASE_URL"`
	OTLPEndpoint    string        `field:"OTEL_EXPORTER_OTLP_ENDPOINT,optional"`
	ShutdownTimeout time.Duration `field:"SHUTDOWN_TIMEOUT,optional"`

	AuthJWKSURL  string `field:"AUTH_JWKS_URL,optional"`
	AuthIssuer   string `field:"AUTH_ISSUER,optional"`
	AuthAudience string `field:"AUTH_AUDIENCE,optional"`
	AuthDev      bool   `field:"AUTH_DEV,optional"`

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
	if cfg.LogLevel == "" {
		cfg.LogLevel = defaultLogLevel
	}
	if cfg.OTLPEndpoint == "" {
		cfg.OTLPEndpoint = defaultOTLPEndpoint
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = defaultShutdownTimeout
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
			cfg.AuthJWKSURL = devJWKSURL()
		}
	}

	return cfg, nil
}

func devJWKSURL() string { return "http://" + devAuthHost + devJWKSPath }

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

func (c Config) Validate() error {
	if c.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	return nil
}
