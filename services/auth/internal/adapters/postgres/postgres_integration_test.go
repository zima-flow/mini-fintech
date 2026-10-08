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
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/adapters/postgres"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/migrations"
)

func testNow() time.Time { return time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC) }

func startAuthPostgres(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()

	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("auth"),
		tcpostgres.WithUsername("auth_app"),
		tcpostgres.WithPassword("auth_app"),
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

func seedUser(t *testing.T, pool *pgxpool.Pool) domain.User {
	t.Helper()

	user := domain.User{
		ID:           id.UUIDv7{}.New(),
		Email:        "user-" + id.UUIDv7{}.New() + "@example.com",
		PasswordHash: "phc-hash",
		Role:         domain.RoleClient,
		CreatedAt:    testNow(),
		UpdatedAt:    testNow(),
	}
	require.NoError(t, postgres.NewUserRepo(pool).Create(context.Background(), user))
	return user
}

func TestTransactor_CommitPersistsRollbackDiscards(t *testing.T) {
	ctx := context.Background()
	pool := startAuthPostgres(t, ctx)
	tx := postgres.NewTransactor(pool)
	repo := postgres.NewUserRepo(pool)

	committed := domain.User{
		ID: id.UUIDv7{}.New(), Email: "commit@example.com", PasswordHash: "h",
		Role: domain.RoleOfficer, CreatedAt: testNow(), UpdatedAt: testNow(),
	}
	require.NoError(t, tx.WithinTx(ctx, func(ctx context.Context, uow domain.UnitOfWork) error {
		return uow.Users().Create(ctx, committed)
	}))
	got, err := repo.ByEmail(ctx, committed.Email)
	require.NoError(t, err)
	require.Equal(t, committed, got)

	rolledBack := domain.User{
		ID: id.UUIDv7{}.New(), Email: "rollback@example.com", PasswordHash: "h",
		Role: domain.RoleClient, CreatedAt: testNow(), UpdatedAt: testNow(),
	}
	boom := errors.New("boom")
	require.ErrorIs(t, tx.WithinTx(ctx, func(ctx context.Context, uow domain.UnitOfWork) error {
		if err := uow.Users().Create(ctx, rolledBack); err != nil {
			return err
		}
		return boom
	}), boom)

	_, err = repo.ByEmail(ctx, rolledBack.Email)
	require.ErrorIs(t, err, errs.ErrNotFound)
}
