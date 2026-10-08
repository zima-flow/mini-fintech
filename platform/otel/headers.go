package otel

import (
	"context"

	"go.opentelemetry.io/otel/propagation"
)

func InjectHeaders(ctx context.Context, propagator propagation.TextMapPropagator) map[string]string {
	if propagator == nil {
		return nil
	}
	carrier := propagation.MapCarrier{}
	propagator.Inject(ctx, carrier)
	if len(carrier) == 0 {
		return nil
	}
	headers := make(map[string]string, len(carrier))
	for key, value := range carrier {
		headers[key] = value
	}
	return headers
}

func ContextWithHeaders(ctx context.Context, propagator propagation.TextMapPropagator, headers map[string]string) context.Context {
	if propagator == nil || len(headers) == 0 {
		return ctx
	}
	return propagator.Extract(ctx, propagation.MapCarrier(headers))
}
