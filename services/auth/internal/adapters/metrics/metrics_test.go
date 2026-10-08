package metrics_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	metricsadapter "github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/adapters/metrics"
)

func sumOf(t *testing.T, rm metricdata.ResourceMetrics, name string) (metricdata.Sum[int64], bool) {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			return sum, ok
		}
	}
	return metricdata.Sum[int64]{}, false
}

func TestRecorder_RegistrationCreated(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	recorder := metricsadapter.NewRecorder(provider.Meter("test"))
	recorder.RegistrationCreated(context.Background())
	recorder.RegistrationCreated(context.Background())

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	sum, ok := sumOf(t, rm, "auth.registrations")
	require.True(t, ok, "auth.registrations must be an int64 counter")
	require.Len(t, sum.DataPoints, 1)
	require.Equal(t, int64(2), sum.DataPoints[0].Value)
}

func TestRecorder_NilMeterIsNoop(t *testing.T) {
	t.Parallel()

	recorder := metricsadapter.NewRecorder(nil)
	require.NotPanics(t, func() { recorder.RegistrationCreated(context.Background()) })
}
