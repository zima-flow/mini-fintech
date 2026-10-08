package interceptors

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

type metrics struct {
	requests metric.Int64Counter
	duration metric.Float64Histogram
}

const (
	metricRequests = "grpc.server.requests"
	metricDuration = "grpc.server.duration"
)

func newMetrics(meter metric.Meter, log *slog.Logger) *metrics {
	if meter == nil {
		return nil
	}
	m := &metrics{}
	requests, err := meter.Int64Counter(
		metricRequests,
		metric.WithDescription("Total server-side gRPC requests (R)"),
	)
	if err != nil {
		log.Error("metrics: create counter", slog.String("instrument", metricRequests), slog.Any("error", err))
	} else {
		m.requests = requests
	}
	duration, err := meter.Float64Histogram(
		metricDuration,
		metric.WithDescription("Server-side gRPC request duration (D)"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		log.Error("metrics: create histogram", slog.String("instrument", metricDuration), slog.Any("error", err))
	} else {
		m.duration = duration
	}
	return m
}

func (m *metrics) record(ctx context.Context, method string, duration time.Duration, err error) {
	if m == nil {
		return
	}
	attrs := metric.WithAttributes(
		attribute.String("rpc.method", method),
		attribute.Int("rpc.grpc.status_code", int(status.Code(err))),
	)
	if m.requests != nil {
		m.requests.Add(ctx, 1, attrs)
	}
	if m.duration != nil {
		m.duration.Record(ctx, float64(duration.Milliseconds()), attrs)
	}
}

func unaryMetrics(m *metrics) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		mark(ctx, nameMetrics)
		start := time.Now()
		resp, err := handler(ctx, req)
		m.record(ctx, info.FullMethod, time.Since(start), err)
		return resp, err
	}
}

func streamMetrics(m *metrics) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		mark(ss.Context(), nameMetrics)
		start := time.Now()
		err := handler(srv, ss)
		m.record(ss.Context(), info.FullMethod, time.Since(start), err)
		return err
	}
}
