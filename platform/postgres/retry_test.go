package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func TestDBTXInterface(t *testing.T) {
	t.Parallel()

	var (
		_ DBTX = (*pgxpool.Pool)(nil)
		_ DBTX = (pgx.Tx)(nil)
	)
}

type stubDBTX struct{ id string }

func (stubDBTX) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (stubDBTX) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }

func (stubDBTX) QueryRow(context.Context, string, ...any) pgx.Row { return nil }

func TestTxFrom(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fallback := stubDBTX{id: "pool"}
	ambient := stubDBTX{id: "tx"}

	require.Equal(t, fallback, TxFrom(ctx, fallback), "no ambient tx falls back to the pool")
	require.Equal(t, ambient, TxFrom(withTx(ctx, ambient), fallback), "ambient tx wins over the pool")
}

func TestIsRetryable(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		err  error
		want bool
	}{
		"deadlock":         {&pgconn.PgError{Code: sqlStateDeadlock}, true},
		"serialization":    {&pgconn.PgError{Code: sqlStateSerialization}, true},
		"wrapped_deadlock": {fmt.Errorf("begin: %w", &pgconn.PgError{Code: sqlStateDeadlock}), true},
		"unique_violation": {&pgconn.PgError{Code: "23505"}, false},
		"plain_error":      {errors.New("boom"), false},
		"nil":              {nil, false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, isRetryable(tc.err))
		})
	}
}
