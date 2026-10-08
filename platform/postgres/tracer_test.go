package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func newRecordingTracer(t *testing.T) (trace.Tracer, *tracetest.SpanRecorder) {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	return provider.Tracer("test"), recorder
}

func attrString(t *testing.T, span sdktrace.ReadOnlySpan, key string) string {
	t.Helper()

	for _, attr := range span.Attributes() {
		if string(attr.Key) == key {
			return attr.Value.AsString()
		}
	}
	require.Failf(t, "attribute not found", "span %q has no attribute %q", span.Name(), key)
	return ""
}

func TestQueryTracer_ClientSpanContinuesParent(t *testing.T) {
	t.Parallel()

	tracer, recorder := newRecordingTracer(t)
	queryTracer := newQueryTracer(tracer)
	require.NotNil(t, queryTracer)

	parentCtx, parent := tracer.Start(context.Background(), "request")
	const sql = "SELECT id FROM users WHERE email = $1"
	ctx := queryTracer.TraceQueryStart(parentCtx, nil, pgx.TraceQueryStartData{
		SQL:  sql,
		Args: []any{"user@example.com", 42},
	})
	queryTracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
	parent.End()

	ended := recorder.Ended()
	require.Len(t, ended, 2)

	span := ended[0]
	for _, candidate := range ended {
		if candidate.Name() == sql {
			span = candidate
		}
	}
	require.Equal(t, trace.SpanKindClient, span.SpanKind())
	require.Equal(t, parent.SpanContext().SpanID(), span.Parent().SpanID())
	require.Equal(t, "postgresql", attrString(t, span, "db.system"))
	require.Equal(t, sql, attrString(t, span, "db.statement"))
	require.NotContains(t, attrString(t, span, "db.statement"), "user@example.com")
}

func TestQueryTracer_RecordsError(t *testing.T) {
	t.Parallel()

	tracer, recorder := newRecordingTracer(t)
	queryTracer := newQueryTracer(tracer)

	ctx := queryTracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	queryTracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: errors.New("connection reset")})

	ended := recorder.Ended()
	require.Len(t, ended, 1)
	require.Equal(t, codes.Error, ended[0].Status().Code)
	require.Contains(t, ended[0].Status().Description, "connection reset")
}

func TestQueryTracer_BoundsStatement(t *testing.T) {
	t.Parallel()

	tracer, recorder := newRecordingTracer(t)
	queryTracer := newQueryTracer(tracer)

	long := strings.Repeat("a", maxStatementLen+500)
	ctx := queryTracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: long})
	queryTracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})

	ended := recorder.Ended()
	require.Len(t, ended, 1)
	require.Len(t, attrString(t, ended[0], "db.statement"), maxStatementLen)
}

func TestNewQueryTracer_NilTracer(t *testing.T) {
	t.Parallel()

	require.Nil(t, newQueryTracer(nil))
}
