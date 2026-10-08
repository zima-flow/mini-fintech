package otel_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	platformotel "github.com/zima-flow/go-mentor/mini-fintech/platform/otel"
)

const (
	testTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	testSpanID  = "00f067aa0ba902b7"
)

func sampledContext(t *testing.T) context.Context {
	t.Helper()

	traceID, err := trace.TraceIDFromHex(testTraceID)
	require.NoError(t, err)
	spanID, err := trace.SpanIDFromHex(testSpanID)
	require.NoError(t, err)

	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	return trace.ContextWithSpanContext(context.Background(), sc)
}

func TestInjectHeaders_ExportsTraceparent(t *testing.T) {
	t.Parallel()

	headers := platformotel.InjectHeaders(sampledContext(t), propagation.TraceContext{})
	require.Equal(t, "00-"+testTraceID+"-"+testSpanID+"-01", headers["traceparent"])
}

func TestInjectHeaders_NoTraceContext_ReturnsNil(t *testing.T) {
	t.Parallel()

	require.Nil(t, platformotel.InjectHeaders(context.Background(), propagation.TraceContext{}))
	require.Nil(t, platformotel.InjectHeaders(sampledContext(t), nil))
}

func TestContextWithHeaders_ExtractsTraceID(t *testing.T) {
	t.Parallel()

	headers := map[string]string{"traceparent": "00-" + testTraceID + "-" + testSpanID + "-01"}
	ctx := platformotel.ContextWithHeaders(context.Background(), propagation.TraceContext{}, headers)
	require.Equal(t, testTraceID, trace.SpanContextFromContext(ctx).TraceID().String())
}

func TestContextWithHeaders_NilOrEmpty_LeavesContext(t *testing.T) {
	t.Parallel()

	empty := platformotel.ContextWithHeaders(context.Background(), propagation.TraceContext{}, nil)
	require.False(t, trace.SpanContextFromContext(empty).IsValid())

	nilPropagator := platformotel.ContextWithHeaders(context.Background(), nil, map[string]string{"traceparent": "x"})
	require.False(t, trace.SpanContextFromContext(nilPropagator).IsValid())
}
