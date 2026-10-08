//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/migrate"
	platformpostgres "github.com/zima-flow/go-mentor/mini-fintech/platform/postgres"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/adapters/postgres"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/migrations"
)

func testNow() time.Time { return time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC) }

func startCustomerPostgres(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()

	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("customer"),
		tcpostgres.WithUsername("customer_app"),
		tcpostgres.WithPassword("customer_app"),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	pool, err := platformpostgres.NewPool(ctx, platformpostgres.Config{DSN: dsn})
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	require.NoError(t, migrate.Up(ctx, pool, migrations.FS, "."))
	return pool
}

func seedCustomer(t *testing.T, pool *pgxpool.Pool, status domain.CustomerStatus, createdAt time.Time) domain.Customer {
	t.Helper()

	customer := domain.Customer{
		ID:        id.UUIDv7{}.New(),
		UserID:    id.UUIDv7{}.New(),
		Status:    status,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
	require.NoError(t, postgres.NewCustomerRepo(pool).Create(context.Background(), customer))
	return customer
}

func TestTransactor_CommitPersistsRollbackDiscards(t *testing.T) {
	ctx := context.Background()
	pool := startCustomerPostgres(t, ctx)
	tx := postgres.NewTransactor(pool)
	repo := postgres.NewCustomerRepo(pool)

	committed := domain.Customer{
		ID:        id.UUIDv7{}.New(),
		UserID:    id.UUIDv7{}.New(),
		Status:    domain.StatusNew,
		CreatedAt: testNow(),
		UpdatedAt: testNow(),
	}
	require.NoError(t, tx.WithinTx(ctx, func(ctx context.Context, uow domain.UnitOfWork) error {
		return uow.Customers().Create(ctx, committed)
	}))
	got, err := repo.ByUserID(ctx, committed.UserID)
	require.NoError(t, err)
	require.Equal(t, committed, got)

	rolledBack := domain.Customer{
		ID:        id.UUIDv7{}.New(),
		UserID:    id.UUIDv7{}.New(),
		Status:    domain.StatusNew,
		CreatedAt: testNow(),
		UpdatedAt: testNow(),
	}
	boom := errors.New("boom")
	require.ErrorIs(t, tx.WithinTx(ctx, func(ctx context.Context, uow domain.UnitOfWork) error {
		if err := uow.Customers().Create(ctx, rolledBack); err != nil {
			return err
		}
		return boom
	}), boom)

	_, err = repo.ByUserID(ctx, rolledBack.UserID)
	require.ErrorIs(t, err, errs.ErrNotFound)
}
