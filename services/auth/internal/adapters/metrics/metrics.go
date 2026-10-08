package metrics

import (
	"context"

	"go.opentelemetry.io/otel/metric"

	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

type Recorder struct {
	registrations metric.Int64Counter
}

var _ domain.Metrics = (*Recorder)(nil)

func NewRecorder(meter metric.Meter) *Recorder {
	r := &Recorder{}
	if meter == nil {
		return r
	}
	r.registrations, _ = meter.Int64Counter("auth.registrations",
		metric.WithDescription("CLIENT accounts created by Register"))
	return r
}

func (r *Recorder) RegistrationCreated(ctx context.Context) {
	if r == nil || r.registrations == nil {
		return
	}
	r.registrations.Add(ctx, 1)
}
