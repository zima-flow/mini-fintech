package gatewayhttp

import (
	"io"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
)

type Config struct {
	Logger     *slog.Logger
	Tracer     trace.Tracer
	Propagator propagation.TextMapPropagator
	RequestID  id.Generator
	Meter      metric.Meter
}

func NewRouter(cfg Config, next http.Handler) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return recovery(cfg, requestID(cfg, tracing(cfg, metrics(cfg, logging(cfg, next)))))
}
