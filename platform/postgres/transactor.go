package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	sqlStateDeadlock      = "40P01"
	sqlStateSerialization = "40001"
)

const DefaultMaxAttempts = 3

type Transactor struct {
	pool        *pgxpool.Pool
	maxAttempts int
}

func NewTransactor(pool *pgxpool.Pool) *Transactor {
	return &Transactor{pool: pool, maxAttempts: DefaultMaxAttempts}
}

func (t *Transactor) WithinTx(ctx context.Context, fn func(ctx context.Context, tx DBTX) error) error {
	var lastErr error

	for attempt := 1; attempt <= t.maxAttempts; attempt++ {
		err := t.attempt(ctx, fn)
		if err == nil {
			return nil
		}
		if !isRetryable(err) {
			return err
		}
		lastErr = err
	}

	return fmt.Errorf("postgres: transaction failed after %d attempts: %w", t.maxAttempts, lastErr)
}

func (t *Transactor) attempt(ctx context.Context, fn func(context.Context, DBTX) error) error {
	tx, err := t.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("postgres: begin: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	if err := fn(withTx(ctx, tx), tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit: %w", err)
	}
	committed = true

	return nil
}

func isRetryable(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}

	switch pgErr.Code {
	case sqlStateDeadlock, sqlStateSerialization:
		return true
	default:
		return false
	}
}
