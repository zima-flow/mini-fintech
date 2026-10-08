package gatewayhttp

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	metricHTTPRequests = "http.server.requests"
	metricHTTPDuration = "http.server.duration"
	routeUnmatched     = "unmatched"
)

type httpMetrics struct {
	requests metric.Int64Counter
	duration metric.Float64Histogram
}

func newHTTPMetrics(meter metric.Meter, logger *slog.Logger) *httpMetrics {
	if meter == nil {
		return nil
	}
	m := &httpMetrics{}
	requests, err := meter.Int64Counter(metricHTTPRequests,
		metric.WithDescription("Total server-side HTTP requests (R)"))
	if err != nil {
		logger.Error("metrics: create counter", slog.String("instrument", metricHTTPRequests), slog.Any("error", err))
	} else {
		m.requests = requests
	}
	duration, err := meter.Float64Histogram(metricHTTPDuration,
		metric.WithDescription("Server-side HTTP request duration (D)"),
		metric.WithUnit("ms"))
	if err != nil {
		logger.Error("metrics: create histogram", slog.String("instrument", metricHTTPDuration), slog.Any("error", err))
	} else {
		m.duration = duration
	}
	return m
}

func (m *httpMetrics) record(ctx context.Context, method, route string, status int, d time.Duration) {
	if m == nil {
		return
	}
	attrs := metric.WithAttributes(
		attribute.String("http.request.method", method),
		attribute.String("http.route", route),
		attribute.Int("http.response.status_code", status),
	)
	if m.requests != nil {
		m.requests.Add(ctx, 1, attrs)
	}
	if m.duration != nil {
		m.duration.Record(ctx, float64(d.Milliseconds()), attrs)
	}
}

func metrics(cfg Config, next http.Handler) http.Handler {
	m := newHTTPMetrics(cfg.Meter, cfg.Logger)
	if m == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)

		status := http.StatusOK
		if sw, ok := w.(*statusWriter); ok && sw.wroteHeader {
			status = sw.status
		}
		route := r.Pattern
		if route == "" {
			route = routeUnmatched
		}
		m.record(r.Context(), r.Method, route, status, time.Since(start))
	})
}
