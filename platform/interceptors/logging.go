package interceptors

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func unaryLogging(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		mark(ctx, nameLogging)
		start := time.Now()
		resp, err := handler(ctx, req)
		logRequest(log, ctx, info.FullMethod, time.Since(start), err)
		return resp, err
	}
}

func streamLogging(log *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		mark(ss.Context(), nameLogging)
		start := time.Now()
		err := handler(srv, ss)
		logRequest(log, ss.Context(), info.FullMethod, time.Since(start), err)
		return err
	}
}

func logRequest(log *slog.Logger, ctx context.Context, method string, duration time.Duration, err error) {
	code := status.Code(err)
	attrs := []slog.Attr{
		slog.String("method", method),
		slog.Int64("duration_ms", duration.Milliseconds()),
		slog.String("status", code.String()),
		slog.String("request_id", RequestIDFromContext(ctx)),
		slog.String("trace_id", traceIDFromContext(ctx)),
	}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	log.LogAttrs(ctx, levelForCode(code), "grpc request", attrs...)
}

func levelForCode(code codes.Code) slog.Level {
	switch code {
	case codes.OK:
		return slog.LevelInfo
	case codes.Internal, codes.Unavailable, codes.DataLoss, codes.Unknown:
		return slog.LevelError
	default:
		return slog.LevelWarn
	}
}

func traceIDFromContext(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	return sc.TraceID().String()
}
