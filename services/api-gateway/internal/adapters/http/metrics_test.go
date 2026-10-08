package gatewayhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
)

func findMetric(t *testing.T, rm metricdata.ResourceMetrics, name string) metricdata.Metrics {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m
			}
		}
	}
	t.Fatalf("metric %q was not exported", name)
	return metricdata.Metrics{}
}

func attrOf(t *testing.T, set attribute.Set, key string) attribute.Value {
	t.Helper()
	value, ok := set.Value(attribute.Key(key))
	require.Truef(t, ok, "attribute %q is missing", key)
	return value
}

func TestRouter_RecordsHTTPMetrics(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/thing", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	router := NewRouter(Config{
		Logger:    discardLogger(),
		RequestID: id.UUIDv7{},
		Meter:     provider.Meter("test"),
	}, mux)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/thing", nil))
	require.Equal(t, http.StatusTeapot, rec.Code)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	sum, ok := findMetric(t, rm, "http.server.requests").Data.(metricdata.Sum[int64])
	require.True(t, ok, "http.server.requests must be an int64 counter")
	require.Len(t, sum.DataPoints, 1)
	require.Equal(t, int64(1), sum.DataPoints[0].Value)
	require.Equal(t, "GET", attrOf(t, sum.DataPoints[0].Attributes, "http.request.method").AsString())
	require.Equal(t, "GET /v1/thing", attrOf(t, sum.DataPoints[0].Attributes, "http.route").AsString())
	require.Equal(t, int64(http.StatusTeapot), attrOf(t, sum.DataPoints[0].Attributes, "http.response.status_code").AsInt64())

	duration, ok := findMetric(t, rm, "http.server.duration").Data.(metricdata.Histogram[float64])
	require.True(t, ok, "http.server.duration must be a float64 histogram")
	require.Len(t, duration.DataPoints, 1)
	require.Equal(t, uint64(1), duration.DataPoints[0].Count)
}

func TestRouter_MetricsCountServerErrors(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/thing", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	router := NewRouter(Config{Logger: discardLogger(), RequestID: id.UUIDv7{}, Meter: provider.Meter("test")}, mux)

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/thing", nil))

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	sum, ok := findMetric(t, rm, "http.server.requests").Data.(metricdata.Sum[int64])
	require.True(t, ok)
	require.Len(t, sum.DataPoints, 1)
	require.Equal(t, int64(http.StatusBadGateway), attrOf(t, sum.DataPoints[0].Attributes, "http.response.status_code").AsInt64())
}

func TestRouter_TracingStartsServerSpanAndContinues(t *testing.T) {
	t.Parallel()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	propagator := propagation.TraceContext{}

	var outbound map[string]string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		carrier := propagation.MapCarrier{}
		propagator.Inject(r.Context(), carrier)
		outbound = map[string]string(carrier)
		w.WriteHeader(http.StatusOK)
	})
	router := NewRouter(Config{
		Logger:     discardLogger(),
		RequestID:  id.UUIDv7{},
		Tracer:     provider.Tracer("test"),
		Propagator: propagator,
	}, handler)

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/auth/register", nil))

	ended := recorder.Ended()
	require.Len(t, ended, 1)
	require.Equal(t, trace.SpanKindServer, ended[0].SpanKind())

	sc := trace.SpanContextFromContext(propagator.Extract(context.Background(), propagation.MapCarrier(outbound)))
	require.Equal(t, ended[0].SpanContext().TraceID(), sc.TraceID())
	require.Equal(t, ended[0].SpanContext().SpanID(), sc.SpanID())
}
