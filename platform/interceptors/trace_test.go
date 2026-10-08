package interceptors

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestRecoveryExtractsTraceparent(t *testing.T) {
	t.Parallel()

	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	cfg := testConfig(publicMethod)
	cfg.Tracer = provider.Tracer("test")
	cfg.Propagator = propagation.TraceContext{}

	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	var got string
	conn := serveProbe(t, Unary(cfg), Stream(cfg), func(ctx context.Context) error {
		got = trace.SpanContextFromContext(ctx).TraceID().String()
		return nil
	})

	incoming := metadata.Pairs("traceparent", "00-"+traceID+"-00f067aa0ba902b7-01")
	ctx := metadata.NewOutgoingContext(context.Background(), incoming)
	require.NoError(t, conn.Invoke(ctx, publicMethod, new(emptypb.Empty), new(emptypb.Empty)))

	require.Equal(t, traceID, got, "server span must continue the caller's trace")
}
