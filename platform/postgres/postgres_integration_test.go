//go:build integration

package postgres_test

import (
	"context"
	"embed"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/clock"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/migrate"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/outbox"
	pg "github.com/zima-flow/go-mentor/mini-fintech/platform/postgres"
)

//go:embed testdata/*.sql
var testMigrations embed.FS

const migrationsDir = "testdata"

func startPostgres(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()

	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("mini"),
		tcpostgres.WithUsername("mini"),
		tcpostgres.WithPassword("mini"),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	pool, err := pg.NewPool(ctx, pg.Config{DSN: dsn})
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return pool
}

func countX(t *testing.T, ctx context.Context, pool *pgxpool.Pool, x int) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM t WHERE x = $1", x).Scan(&n))
	return n
}

func countOutbox(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id string) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM outbox WHERE id = $1", id).Scan(&n))
	return n
}

func countUnpublished(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM outbox WHERE published_at IS NULL").Scan(&n))
	return n
}

func TestTransactor(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t, ctx)
	require.NoError(t, migrate.Up(ctx, pool, testMigrations, migrationsDir))
	tx := pg.NewTransactor(pool)

	const (
		commitX         = 1
		rollbackX       = 2
		outboxCommitX   = 3
		outboxRollbackX = 4
		retryX          = 5
	)

	t.Run("Commit", func(t *testing.T) {
		err := tx.WithinTx(ctx, func(ctx context.Context, db pg.DBTX) error {
			_, err := db.Exec(ctx, "INSERT INTO t (x) VALUES ($1)", commitX)
			return err
		})
		require.NoError(t, err)
		require.Equal(t, 1, countX(t, ctx, pool, commitX))
	})

	t.Run("RollbackOnError", func(t *testing.T) {
		sentinel := errors.New("domain failure")

		err := tx.WithinTx(ctx, func(ctx context.Context, db pg.DBTX) error {
			if _, err := db.Exec(ctx, "INSERT INTO t (x) VALUES ($1)", rollbackX); err != nil {
				return err
			}
			return sentinel
		})

		require.ErrorIs(t, err, sentinel)
		require.Equal(t, 0, countX(t, ctx, pool, rollbackX))
	})

	t.Run("OutboxCommitsWithState", func(t *testing.T) {
		msg := outbox.Message{
			ID:         "0192f0a0-0000-7000-8000-000000000003",
			Topic:      "kyc.events",
			EventType:  "kyc.application_approved",
			Payload:    []byte(`{"application_id":"a1"}`),
			OccurredAt: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC),
		}

		err := tx.WithinTx(ctx, func(ctx context.Context, db pg.DBTX) error {
			if _, err := db.Exec(ctx, "INSERT INTO t (x) VALUES ($1)", outboxCommitX); err != nil {
				return err
			}
			return outbox.Enqueue(ctx, db, msg)
		})

		require.NoError(t, err)
		require.Equal(t, 1, countX(t, ctx, pool, outboxCommitX))
		require.Equal(t, 1, countOutbox(t, ctx, pool, msg.ID))
	})

	t.Run("OutboxRollsBackWithState", func(t *testing.T) {
		msg := outbox.Message{
			ID:         "0192f0a0-0000-7000-8000-000000000004",
			Topic:      "kyc.events",
			EventType:  "kyc.application_approved",
			Payload:    []byte(`{"application_id":"a2"}`),
			OccurredAt: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC),
		}
		sentinel := errors.New("abort after enqueue")

		err := tx.WithinTx(ctx, func(ctx context.Context, db pg.DBTX) error {
			if _, err := db.Exec(ctx, "INSERT INTO t (x) VALUES ($1)", outboxRollbackX); err != nil {
				return err
			}
			if err := outbox.Enqueue(ctx, db, msg); err != nil {
				return err
			}
			return sentinel
		})

		require.ErrorIs(t, err, sentinel)
		require.Equal(t, 0, countX(t, ctx, pool, outboxRollbackX))
		require.Equal(t, 0, countOutbox(t, ctx, pool, msg.ID))
	})

	t.Run("RetriesWholeTransactionOnSerializationFailure", func(t *testing.T) {
		attempts := 0

		err := tx.WithinTx(ctx, func(ctx context.Context, db pg.DBTX) error {
			attempts++
			if attempts == 1 {
				return &pgconn.PgError{Code: "40001", Message: "serialization failure"}
			}
			_, err := db.Exec(ctx, "INSERT INTO t (x) VALUES ($1)", retryX)
			return err
		})

		require.NoError(t, err)
		require.Equal(t, 2, attempts)
		require.Equal(t, 1, countX(t, ctx, pool, retryX))
	})

	t.Run("GivesUpAfterMaxAttempts", func(t *testing.T) {
		attempts := 0

		err := tx.WithinTx(ctx, func(context.Context, pg.DBTX) error {
			attempts++
			return &pgconn.PgError{Code: "40P01", Message: "deadlock detected"}
		})

		require.Error(t, err)
		require.Equal(t, pg.DefaultMaxAttempts, attempts)
	})

	t.Run("MigrateIsIdempotent", func(t *testing.T) {
		require.NoError(t, migrate.Up(ctx, pool, testMigrations, migrationsDir))
	})
}

type publishedMessage struct {
	topic   string
	headers map[string]string
	payload []byte
}

type fakePublisher struct {
	messages []publishedMessage
}

func (f *fakePublisher) Publish(_ context.Context, topic string, headers map[string]string, payload []byte) error {
	f.messages = append(f.messages, publishedMessage{topic: topic, headers: headers, payload: payload})
	return nil
}

func TestRelayPublishPending(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t, ctx)
	require.NoError(t, migrate.Up(ctx, pool, testMigrations, migrationsDir))

	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	ids := []string{
		"0192f0a0-0000-7000-8000-000000000005",
		"0192f0a0-0000-7000-8000-000000000006",
	}
	headers := map[string]string{
		"traceparent":  "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"x-request-id": "req-relay",
	}
	for i, id := range ids {
		require.NoError(t, outbox.Enqueue(ctx, pool, outbox.Message{
			ID:         id,
			Topic:      "ledger.events",
			EventType:  "ledger.transfer_posted",
			Headers:    headers,
			Payload:    []byte{byte('0' + i)},
			OccurredAt: now.Add(time.Duration(i) * time.Second),
		}))
	}

	pub := &fakePublisher{}
	relay := outbox.NewRelay(outbox.RelayConfig{DB: pool, Publisher: pub, Clock: clock.Fixed{T: now.Add(time.Hour)}})

	n, err := relay.PublishPending(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.Len(t, pub.messages, 2)
	require.Equal(t, "ledger.events", pub.messages[0].topic)
	require.Equal(t, headers, pub.messages[0].headers, "headers must survive a real jsonb round-trip")
	require.Equal(t, 0, countUnpublished(t, ctx, pool))

	n, err = relay.PublishPending(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, n)
}

func outboxHeaders(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id string) map[string]string {
	t.Helper()
	var h map[string]string
	require.NoError(t, pool.QueryRow(ctx, "SELECT headers FROM outbox WHERE id = $1", id).Scan(&h))
	return h
}

func TestOutboxHeadersRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t, ctx)
	require.NoError(t, migrate.Up(ctx, pool, testMigrations, migrationsDir))

	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	headers := map[string]string{
		"traceparent":  "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"x-request-id": "req-outbox",
	}
	const id = "0192f0a0-0000-7000-8000-000000000007"

	require.NoError(t, outbox.Enqueue(ctx, pool, outbox.Message{
		ID:         id,
		Topic:      "auth.events",
		EventType:  "auth.user_registered",
		Headers:    headers,
		Payload:    []byte(`{"user_id":"u1"}`),
		OccurredAt: now,
	}))

	require.Equal(t, headers, outboxHeaders(t, ctx, pool, id))

	pub := &fakePublisher{}
	relay := outbox.NewRelay(outbox.RelayConfig{DB: pool, Publisher: pub, Clock: clock.Fixed{T: now.Add(time.Hour)}})

	n, err := relay.PublishPending(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Len(t, pub.messages, 1)
	require.Equal(t, headers, pub.messages[0].headers)
	require.Equal(t, 0, countUnpublished(t, ctx, pool))
}
