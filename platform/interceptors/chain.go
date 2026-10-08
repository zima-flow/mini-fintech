package interceptors

import (
	"io"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/authn"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
)

const (
	nameRecovery  = "recovery"
	nameRequestID = "requestid"
	nameLogging   = "logging"
	nameMetrics   = "metrics"
	nameAuth      = "auth"
)

const (
	requestIDHeader = "x-request-id"
	authorization   = "authorization"
	bearerPrefix    = "Bearer "
	healthPrefix    = "/grpc.health.v1.Health/"
)

type Config struct {
	Logger     *slog.Logger
	Meter      metric.Meter
	Tracer     trace.Tracer
	Propagator propagation.TextMapPropagator
	RequestID  id.Generator
	Auth       AuthConfig
}

type AuthConfig struct {
	JWKSURL       string
	CacheTTL      time.Duration
	PublicMethods []string
	Issuer        string
	Audience      string
	Dev           bool
}

func (c AuthConfig) Validate() error {
	return authn.Config{Issuer: c.Issuer, Audience: c.Audience, Dev: c.Dev}.Validate()
}

const DefaultCacheTTL = authn.DefaultCacheTTL

type Principal struct {
	Subject    string
	Roles      []string
	CustomerID string
}

func Unary(cfg Config) []grpc.UnaryServerInterceptor {
	log := loggerOrDefault(cfg.Logger)
	m := newMetrics(cfg.Meter, log)
	a := newAuthenticator(cfg.Auth)
	return []grpc.UnaryServerInterceptor{
		unaryRecovery(log, cfg.Tracer, cfg.Propagator),
		unaryRequestID(cfg.RequestID),
		unaryLogging(log),
		unaryMetrics(m),
		unaryAuth(a),
	}
}

func Stream(cfg Config) []grpc.StreamServerInterceptor {
	log := loggerOrDefault(cfg.Logger)
	m := newMetrics(cfg.Meter, log)
	a := newAuthenticator(cfg.Auth)
	return []grpc.StreamServerInterceptor{
		streamRecovery(log, cfg.Tracer, cfg.Propagator),
		streamRequestID(cfg.RequestID),
		streamLogging(log),
		streamMetrics(m),
		streamAuth(a),
	}
}

func loggerOrDefault(log *slog.Logger) *slog.Logger {
	if log != nil {
		return log
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
