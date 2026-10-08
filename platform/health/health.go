package health

import (
	"context"
	"errors"
	"fmt"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
)

type Reporter interface {
	Readiness(ctx context.Context) error
}

type Func func(ctx context.Context) error

func (f Func) Readiness(ctx context.Context) error { return f(ctx) }

type Probe struct {
	Name  string
	Check func(ctx context.Context) error
}

type Registry struct {
	probes []Probe
}

func New(probes ...Probe) *Registry { return &Registry{probes: probes} }

func (r *Registry) Readiness(ctx context.Context) error {
	for _, p := range r.probes {
		if p.Check == nil {
			continue
		}
		if err := p.Check(ctx); err != nil {
			return fmt.Errorf("health: probe %q: %w", p.Name, errors.Join(errs.ErrUnavailable, err))
		}
	}
	return nil
}

type AlwaysReady struct{}

func (AlwaysReady) Readiness(context.Context) error { return nil }
