package interceptors

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
)

func unaryRequestID(gen id.Generator) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		mark(ctx, nameRequestID)
		requestID := requestIDFromIncoming(ctx, gen)
		_ = grpc.SetHeader(ctx, metadata.Pairs(requestIDHeader, requestID))
		return handler(WithRequestID(ctx, requestID), req)
	}
}

func streamRequestID(gen id.Generator) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()
		mark(ctx, nameRequestID)
		requestID := requestIDFromIncoming(ctx, gen)
		_ = grpc.SetHeader(ctx, metadata.Pairs(requestIDHeader, requestID))
		return handler(srv, &contextStream{ServerStream: ss, ctx: WithRequestID(ctx, requestID)})
	}
}

func requestIDFromIncoming(ctx context.Context, gen id.Generator) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get(requestIDHeader); len(vals) > 0 && vals[0] != "" {
			return vals[0]
		}
	}
	if gen == nil {
		gen = id.UUIDv7{}
	}
	return gen.New()
}
