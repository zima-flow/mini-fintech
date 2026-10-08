//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/migrate"
	platformpostgres "github.com/zima-flow/go-mentor/mini-fintech/platform/postgres"
	"github.com/zima-flow/go-mentor/mini-fintech/services/example/internal/adapters/postgres"
	"github.com/zima-flow/go-mentor/mini-fintech/services/example/migrations"
)

func startPostgres(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()

	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("example"),
		tcpostgres.WithUsername("example_app"),
		tcpostgres.WithPassword("example_app"),
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

func TestEchoRepo_RoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t, ctx)

	repo := postgres.NewEchoRepo(platformpostgres.New(pool), id.UUIDv7{})
	at := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)

	got, err := repo.Echo(ctx, "hello", at)
	require.NoError(t, err)
	require.NotEmpty(t, got.ID)
	require.Equal(t, "hello", got.Message)
	require.Equal(t, at, got.At.UTC())

	var (
		storedID        string
		storedMessage   string
		storedCreatedAt time.Time
	)
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT id::text, message, created_at FROM echoes WHERE message = $1", "hello",
	).Scan(&storedID, &storedMessage, &storedCreatedAt))
	require.Equal(t, got.ID, storedID)
	require.Equal(t, "hello", storedMessage)
	require.Equal(t, at, storedCreatedAt.UTC())
}

func TestEchoRepo_DuplicateMessage_AlreadyExists(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t, ctx)

	repo := postgres.NewEchoRepo(platformpostgres.New(pool), id.UUIDv7{})
	at := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)

	_, err := repo.Echo(ctx, "duplicate", at)
	require.NoError(t, err)

	_, err = repo.Echo(ctx, "duplicate", at)
	require.ErrorIs(t, err, errs.ErrAlreadyExists)
}
