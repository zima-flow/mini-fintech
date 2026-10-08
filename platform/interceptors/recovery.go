package interceptors

import (
	"context"
	"log/slog"
	"runtime/debug"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func unaryRecovery(log *slog.Logger, tracer trace.Tracer, propagator propagation.TextMapPropagator) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		mark(ctx, nameRecovery)
		if tracer != nil {
			ctx = extractTrace(ctx, propagator)
			var span trace.Span
			ctx, span = tracer.Start(ctx, info.FullMethod, trace.WithSpanKind(trace.SpanKindServer))
			defer span.End()
		}
		defer func() {
			if r := recover(); r != nil {
				log.ErrorContext(ctx, "panic recovered",
					slog.String("method", info.FullMethod),
					slog.Any("panic", r),
					slog.String("stack", string(debug.Stack())),
				)
				resp, err = nil, status.Error(codes.Internal, "internal error")
			}
		}()
		return handler(ctx, req)
	}
}

func streamRecovery(log *slog.Logger, tracer trace.Tracer, propagator propagation.TextMapPropagator) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		ctx := ss.Context()
		mark(ctx, nameRecovery)
		if tracer != nil {
			ctx = extractTrace(ctx, propagator)
			var span trace.Span
			ctx, span = tracer.Start(ctx, info.FullMethod, trace.WithSpanKind(trace.SpanKindServer))
			defer span.End()
			ss = &contextStream{ServerStream: ss, ctx: ctx}
		}
		defer func() {
			if r := recover(); r != nil {
				log.ErrorContext(ss.Context(), "panic recovered",
					slog.String("method", info.FullMethod),
					slog.Any("panic", r),
					slog.String("stack", string(debug.Stack())),
				)
				err = status.Error(codes.Internal, "internal error")
			}
		}()
		return handler(srv, ss)
	}
}

func extractTrace(ctx context.Context, propagator propagation.TextMapPropagator) context.Context {
	if propagator == nil {
		return ctx
	}
	md, _ := metadata.FromIncomingContext(ctx)
	return propagator.Extract(ctx, metadataCarrier{md: md})
}

type metadataCarrier struct{ md metadata.MD }

func (c metadataCarrier) Get(key string) string {
	values := c.md.Get(key)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (c metadataCarrier) Set(key, value string) { c.md.Set(key, value) }

func (c metadataCarrier) Keys() []string {
	keys := make([]string, 0, len(c.md))
	for key := range c.md {
		keys = append(keys, key)
	}
	return keys
}
