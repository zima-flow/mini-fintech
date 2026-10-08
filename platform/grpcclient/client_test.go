package grpcclient_test

import (
	"context"
	"net"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/grpcclient"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/interceptors"
)

const (
	probeService = "probe.Probe"
	unaryMethod  = "/probe.Probe/Call"
	streamMethod = "/probe.Probe/CallStream"

	testTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	testSpanID  = "00f067aa0ba902b7"
	testTrace   = "00-" + testTraceID + "-" + testSpanID + "-01"
)

type capturedMetadata struct {
	mu sync.Mutex
	md metadata.MD
}

func (c *capturedMetadata) set(md metadata.MD) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.md = md
}

func (c *capturedMetadata) get() metadata.MD {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.md.Copy()
}

func serveProbe(t *testing.T, cfg grpcclient.Config) (*grpc.ClientConn, *capturedMetadata) {
	t.Helper()

	captured := &capturedMetadata{}
	desc := grpc.ServiceDesc{
		ServiceName: probeService,
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{{
			MethodName: "Call",
			Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
				in := new(emptypb.Empty)
				if err := dec(in); err != nil {
					return nil, err
				}
				handler := func(ctx context.Context, _ any) (any, error) {
					captured.set(incomingMetadata(ctx))
					return new(emptypb.Empty), nil
				}
				if interceptor == nil {
					return handler(ctx, in)
				}
				return interceptor(ctx, in, &grpc.UnaryServerInfo{Server: srv, FullMethod: unaryMethod}, handler)
			},
		}},
		Streams: []grpc.StreamDesc{{
			StreamName:    "CallStream",
			ServerStreams: true,
			Handler: func(_ any, ss grpc.ServerStream) error {
				captured.set(incomingMetadata(ss.Context()))
				return ss.SendMsg(new(emptypb.Empty))
			},
		}},
	}

	srv := grpc.NewServer()
	srv.RegisterService(&desc, struct{}{})

	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(grpcclient.Unary(cfg)),
		grpc.WithChainStreamInterceptor(grpcclient.Stream(cfg)),
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = conn.Close()
		srv.Stop()
	})

	return conn, captured
}

func incomingMetadata(ctx context.Context) metadata.MD {
	md, _ := metadata.FromIncomingContext(ctx)
	return md.Copy()
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

func TestUnary_InjectsTraceparentAndRequestID(t *testing.T) {
	t.Parallel()

	conn, captured := serveProbe(t, grpcclient.Config{Propagator: propagation.TraceContext{}})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext(t))
	ctx = interceptors.WithRequestID(ctx, "req-unary")

	require.NoError(t, conn.Invoke(ctx, unaryMethod, new(emptypb.Empty), new(emptypb.Empty)))

	md := captured.get()
	require.Equal(t, []string{testTrace}, md.Get("traceparent"))
	require.Equal(t, []string{"req-unary"}, md.Get("x-request-id"))
}

func TestUnary_AbsentSpanAndRequestIDAddsNoHeaders(t *testing.T) {
	t.Parallel()

	conn, captured := serveProbe(t, grpcclient.Config{Propagator: propagation.TraceContext{}})

	require.NoError(t, conn.Invoke(context.Background(), unaryMethod, new(emptypb.Empty), new(emptypb.Empty)))

	md := captured.get()
	require.Empty(t, md.Get("traceparent"))
	require.Empty(t, md.Get("x-request-id"))
}

func TestUnary_NilPropagatorIsNoOp(t *testing.T) {
	t.Parallel()

	conn, captured := serveProbe(t, grpcclient.Config{})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext(t))

	require.NoError(t, conn.Invoke(ctx, unaryMethod, new(emptypb.Empty), new(emptypb.Empty)))

	md := captured.get()
	require.Empty(t, md.Get("traceparent"), "a nil propagator must not inject a trace context")
}

func TestUnary_NilPropagatorStillForwardsRequestID(t *testing.T) {
	t.Parallel()

	conn, captured := serveProbe(t, grpcclient.Config{})
	ctx := interceptors.WithRequestID(context.Background(), "req-9")

	require.NoError(t, conn.Invoke(ctx, unaryMethod, new(emptypb.Empty), new(emptypb.Empty)))

	md := captured.get()
	require.Empty(t, md.Get("traceparent"))
	require.Equal(t, []string{"req-9"}, md.Get("x-request-id"), "trace and request-id injection are independent")
}

func TestStream_InjectsTraceparentAndRequestID(t *testing.T) {
	t.Parallel()

	conn, captured := serveProbe(t, grpcclient.Config{Propagator: propagation.TraceContext{}})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext(t))
	ctx = interceptors.WithRequestID(ctx, "req-stream")

	stream, err := conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, streamMethod)
	require.NoError(t, err)
	require.NoError(t, stream.RecvMsg(new(emptypb.Empty)))

	md := captured.get()
	require.Equal(t, []string{testTrace}, md.Get("traceparent"))
	require.Equal(t, []string{"req-stream"}, md.Get("x-request-id"))
}

func TestStream_AbsentSpanAndRequestIDAddsNoHeaders(t *testing.T) {
	t.Parallel()

	conn, captured := serveProbe(t, grpcclient.Config{Propagator: propagation.TraceContext{}})

	stream, err := conn.NewStream(context.Background(), &grpc.StreamDesc{ServerStreams: true}, streamMethod)
	require.NoError(t, err)
	require.NoError(t, stream.RecvMsg(new(emptypb.Empty)))

	md := captured.get()
	require.Empty(t, md.Get("traceparent"))
	require.Empty(t, md.Get("x-request-id"))
}

func TestUnary_ForwardsAuthorization(t *testing.T) {
	t.Parallel()

	conn, captured := serveProbe(t, grpcclient.Config{Propagator: propagation.TraceContext{}})
	ctx := interceptors.WithAuthorization(context.Background(), "Bearer edge-token")

	require.NoError(t, conn.Invoke(ctx, unaryMethod, new(emptypb.Empty), new(emptypb.Empty)))

	md := captured.get()
	require.Equal(t, []string{"Bearer edge-token"}, md.Get("authorization"))
}

func TestUnary_AbsentAuthorizationAddsNoHeader(t *testing.T) {
	t.Parallel()

	conn, captured := serveProbe(t, grpcclient.Config{Propagator: propagation.TraceContext{}})

	require.NoError(t, conn.Invoke(context.Background(), unaryMethod, new(emptypb.Empty), new(emptypb.Empty)))

	require.Empty(t, captured.get().Get("authorization"))
}
