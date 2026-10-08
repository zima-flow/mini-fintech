package health

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
)

func TestRegistry_Readiness(t *testing.T) {
	t.Parallel()

	t.Run("all_probes_pass", func(t *testing.T) {
		t.Parallel()
		r := New(
			Probe{Name: "db", Check: func(context.Context) error { return nil }},
			Probe{Name: "cache", Check: func(context.Context) error { return nil }},
		)
		require.NoError(t, r.Readiness(context.Background()))
	})

	t.Run("failing_probe_is_unavailable_and_named", func(t *testing.T) {
		t.Parallel()
		r := New(Probe{Name: "db", Check: func(context.Context) error { return errors.New("connection refused") }})
		err := r.Readiness(context.Background())
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrUnavailable)
		require.Contains(t, err.Error(), "db")
	})

	t.Run("nil_check_is_skipped", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, New(Probe{Name: "db"}).Readiness(context.Background()))
	})
}

func TestFunc_Readiness(t *testing.T) {
	t.Parallel()
	var f Func = func(context.Context) error { return errs.ErrUnavailable }
	require.ErrorIs(t, f.Readiness(context.Background()), errs.ErrUnavailable)
}

func TestAlwaysReady(t *testing.T) {
	t.Parallel()
	require.NoError(t, AlwaysReady{}.Readiness(context.Background()))
}
