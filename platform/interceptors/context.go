package interceptors

import (
	"context"

	"google.golang.org/grpc"
)

type requestIDKey struct{}
type principalKey struct{}
type authorizationKey struct{}

func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey{}).(string)
	return v
}

func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, requestID)
}

func AuthorizationFromContext(ctx context.Context) string {
	v, _ := ctx.Value(authorizationKey{}).(string)
	return v
}

func WithAuthorization(ctx context.Context, authorization string) context.Context {
	return context.WithValue(ctx, authorizationKey{}, authorization)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

type orderKey struct{}

func withOrderTrace(ctx context.Context, sink *[]string) context.Context {
	return context.WithValue(ctx, orderKey{}, sink)
}

func mark(ctx context.Context, name string) {
	if sink, ok := ctx.Value(orderKey{}).(*[]string); ok {
		*sink = append(*sink, name)
	}
}

type contextStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *contextStream) Context() context.Context { return s.ctx }
