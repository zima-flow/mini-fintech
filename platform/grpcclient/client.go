package grpcclient

import (
	"context"

	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/interceptors"
)

const (
	requestIDHeader     = "x-request-id"
	authorizationHeader = "authorization"
)

type Config struct {
	Propagator propagation.TextMapPropagator
}

func Unary(cfg Config) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		return invoker(inject(ctx, cfg.Propagator), method, req, reply, cc, opts...)
	}
}

func Stream(cfg Config) grpc.StreamClientInterceptor {
	return func(
		ctx context.Context,
		desc *grpc.StreamDesc,
		cc *grpc.ClientConn,
		method string,
		streamer grpc.Streamer,
		opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		return streamer(inject(ctx, cfg.Propagator), desc, cc, method, opts...)
	}
}

func inject(ctx context.Context, propagator propagation.TextMapPropagator) context.Context {
	var pairs []string

	if propagator != nil {
		carrier := propagation.MapCarrier{}
		propagator.Inject(ctx, carrier)
		for key, value := range carrier {
			pairs = append(pairs, key, value)
		}
	}

	if requestID := interceptors.RequestIDFromContext(ctx); requestID != "" {
		pairs = append(pairs, requestIDHeader, requestID)
	}

	if authorization := interceptors.AuthorizationFromContext(ctx); authorization != "" {
		pairs = append(pairs, authorizationHeader, authorization)
	}

	if len(pairs) == 0 {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, pairs...)
}
