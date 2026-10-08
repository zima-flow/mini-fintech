package otel

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSetup(t *testing.T) {
	t.Parallel()

	t.Run("empty_endpoint_fails_fast", func(t *testing.T) {
		t.Parallel()
		_, err := Setup(context.Background(), Config{ServiceName: "test"})
		require.Error(t, err)
	})

	t.Run("returns_injected_providers_and_shutdown", func(t *testing.T) {
		t.Parallel()
		providers, err := Setup(context.Background(), Config{
			Endpoint:    "127.0.0.1:4317",
			ServiceName: "platform-test",
			Insecure:    true,
		})
		require.NoError(t, err)
		require.NotNil(t, providers.TracerProvider)
		require.NotNil(t, providers.MeterProvider)
		require.NotNil(t, providers.Propagator)
		require.NotNil(t, providers.Shutdown)

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		start := time.Now()
		_ = providers.Shutdown(ctx)
		require.Less(t, time.Since(start), 2*time.Second, "Shutdown must be bounded by its context")
	})
}
