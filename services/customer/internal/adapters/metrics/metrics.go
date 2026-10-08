package metrics

import (
	"context"

	"go.opentelemetry.io/otel/metric"

	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
)

type Recorder struct {
	created metric.Int64Counter
	filled  metric.Int64Counter
}

var _ domain.Metrics = (*Recorder)(nil)

func NewRecorder(meter metric.Meter) *Recorder {
	r := &Recorder{}
	if meter == nil {
		return r
	}
	r.created, _ = meter.Int64Counter("customer.profiles_created",
		metric.WithDescription("Empty NEW profiles created from auth.user_registered"))
	r.filled, _ = meter.Int64Counter("customer.profiles_filled",
		metric.WithDescription("Profiles transitioned NEW -> PROFILE_FILLED"))
	return r
}

func (r *Recorder) ProfileCreated(ctx context.Context) {
	if r == nil || r.created == nil {
		return
	}
	r.created.Add(ctx, 1)
}

func (r *Recorder) ProfileFilled(ctx context.Context) {
	if r == nil || r.filled == nil {
		return
	}
	r.filled.Add(ctx, 1)
}
