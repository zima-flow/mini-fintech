package config

import (
	"errors"
	"time"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/config"
)

const (
	defaultGRPCAddr        = ":50059"
	defaultLogLevel        = "info"
	defaultOTLPEndpoint    = "localhost:4317"
	defaultShutdownTimeout = 10 * time.Second
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

	return cfg, nil
}

func (c Config) Validate() error {
	if c.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	return nil
}
