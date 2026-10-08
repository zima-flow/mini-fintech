package metrics_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	metricsadapter "github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/adapters/metrics"
)

func metricByName(rm metricdata.ResourceMetrics, name string) (metricdata.Metrics, bool) {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m, true
			}
		}
	}
	return metricdata.Metrics{}, false
}

func TestRecorder_Counters(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	recorder := metricsadapter.NewRecorder(provider.Meter("test"))
	recorder.ProfileCreated(context.Background())
	recorder.ProfileFilled(context.Background())
	recorder.ProfileFilled(context.Background())

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	created, ok := metricByName(rm, "customer.profiles_created")
	require.True(t, ok)
	createdSum, ok := created.Data.(metricdata.Sum[int64])
	require.True(t, ok)
	require.Len(t, createdSum.DataPoints, 1)
	require.Equal(t, int64(1), createdSum.DataPoints[0].Value)

	filled, ok := metricByName(rm, "customer.profiles_filled")
	require.True(t, ok)
	filledSum, ok := filled.Data.(metricdata.Sum[int64])
	require.True(t, ok)
	require.Len(t, filledSum.DataPoints, 1)
	require.Equal(t, int64(2), filledSum.DataPoints[0].Value)
}

func TestRecorder_NilMeterIsNoop(t *testing.T) {
	t.Parallel()

	recorder := metricsadapter.NewRecorder(nil)
	require.NotPanics(t, func() {
		recorder.ProfileCreated(context.Background())
		recorder.ProfileFilled(context.Background())
	})
}
