package grpcadapter

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	authv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/auth/v1"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/interceptors"
)

const (
	testTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	testSpanID  = "00f067aa0ba902b7"
	testTrace   = "00-" + testTraceID + "-" + testSpanID + "-01"
)

type recorderAuthService struct {
	authv1.UnimplementedAuthServiceServer

	mu sync.Mutex
	md metadata.MD
}

func (s *recorderAuthService) Register(ctx context.Context, _ *authv1.RegisterRequest) (*authv1.RegisterResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	s.mu.Lock()
	s.md = md.Copy()
	s.mu.Unlock()
	return &authv1.RegisterResponse{}, nil
}

func (s *recorderAuthService) captured() metadata.MD {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.md.Copy()
}

func serveAuth(t *testing.T, propagator propagation.TextMapPropagator) (authv1.AuthServiceClient, *recorderAuthService) {
	t.Helper()

	rec := &recorderAuthService{}
	srv := grpc.NewServer()
	authv1.RegisterAuthServiceServer(srv, rec)

	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()

	conn, err := newConn("passthrough:///bufnet", propagator, grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}))
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = conn.Close()
		srv.Stop()
	})
	return authv1.NewAuthServiceClient(conn), rec
}

func TestNewConn_ForwardsEdgeMetadata(t *testing.T) {
	t.Parallel()

	client, rec := serveAuth(t, propagation.TraceContext{})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext(t))
	ctx = interceptors.WithRequestID(ctx, "req-42")
	ctx = interceptors.WithAuthorization(ctx, "Bearer edge-token")

	_, err := client.Register(ctx, &authv1.RegisterRequest{})
	require.NoError(t, err)

	md := rec.captured()
	require.Equal(t, []string{"Bearer edge-token"}, md.Get("authorization"))
	require.Equal(t, []string{"req-42"}, md.Get("x-request-id"))
	require.Equal(t, []string{testTrace}, md.Get("traceparent"))
}

func TestNewConn_NoAuthorizationAddsNoHeader(t *testing.T) {
	t.Parallel()

	client, rec := serveAuth(t, propagation.TraceContext{})

	_, err := client.Register(context.Background(), &authv1.RegisterRequest{})
	require.NoError(t, err)

	require.Empty(t, rec.captured().Get("authorization"))
}

func TestNewAuthClient_ConstructsAndCloses(t *testing.T) {
	t.Parallel()

	client, err := NewAuthClient(Config{AuthAddr: "passthrough:///auth"})
	require.NoError(t, err)
	require.NotNil(t, client.AuthServiceClient)
	require.NoError(t, client.Close())
}

func TestNewCustomerClient_ConstructsAndCloses(t *testing.T) {
	t.Parallel()

	client, err := NewCustomerClient(Config{CustomerAddr: "passthrough:///customer"})
	require.NoError(t, err)
	require.NotNil(t, client.CustomerServiceClient)
	require.NoError(t, client.Close())
}

func TestNewConn_RejectsEmptyTarget(t *testing.T) {
	t.Parallel()

	_, err := newConn("", nil)
	require.ErrorIs(t, err, errs.ErrInvalidArgument)
}

func TestAuthClient_Ready(t *testing.T) {
	t.Parallel()

	healthSrv := health.NewServer()
	healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	srv := grpc.NewServer()
	grpc_health_v1.RegisterHealthServer(srv, healthSrv)

	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()

	conn, err := newConn("passthrough:///bufnet", nil, grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = conn.Close()
		srv.Stop()
	})

	client := &AuthClient{conn: conn}
	require.NoError(t, client.Ready(context.Background()))

	healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	require.ErrorIs(t, client.Ready(context.Background()), errs.ErrUnavailable)
}

func TestCustomerClient_Ready_Unreachable(t *testing.T) {
	t.Parallel()

	lis := bufconn.Listen(1 << 20)
	conn, err := newConn("passthrough:///bufnet", nil, grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, lis.Close())

	client := &CustomerClient{conn: conn}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.ErrorIs(t, client.Ready(ctx), errs.ErrUnavailable)
}

func spanContext(t *testing.T) trace.SpanContext {
	t.Helper()

	traceID, err := trace.TraceIDFromHex(testTraceID)
	require.NoError(t, err)
	spanID, err := trace.SpanIDFromHex(testSpanID)
	require.NoError(t, err)
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
}
