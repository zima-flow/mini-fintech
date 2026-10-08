//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	pg "github.com/zima-flow/go-mentor/mini-fintech/platform/postgres"
)

func TestNewPool_TracesQueries(t *testing.T) {
	ctx := context.Background()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	tracer := provider.Tracer("test")

	base := startPostgres(t, ctx)
	pool, err := pg.NewPool(ctx, pg.Config{DSN: base.Config().ConnString(), Tracer: tracer})
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	parentCtx, parent := tracer.Start(ctx, "request")
	var one int
	require.NoError(t, pool.QueryRow(parentCtx, "SELECT 1").Scan(&one))
	parent.End()

	var querySpan sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		if span.Name() == "SELECT 1" && span.Parent().SpanID() == parent.SpanContext().SpanID() {
			querySpan = span
		}
	}
	require.NotNil(t, querySpan, "expected a query span parented from the request span")
	require.Equal(t, trace.SpanKindClient, querySpan.SpanKind())
}
