//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/migrate"
	platformpostgres "github.com/zima-flow/go-mentor/mini-fintech/platform/postgres"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/migrations"
)

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

	return pool
}

func TestMigrate_Up_Idempotent(t *testing.T) {
	ctx := context.Background()
	pool := startCustomerPostgres(t, ctx)

	require.NoError(t, migrate.Up(ctx, pool, migrations.FS, "."))
	require.NoError(t, migrate.Up(ctx, pool, migrations.FS, "."))

	for _, table := range []string{"customers", "idempotency_keys", "outbox", "processed_events"} {
		var exists bool
		require.NoError(t, pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", "public."+table).Scan(&exists))
		require.True(t, exists, "table %s must exist after migration", table)
	}

	var indexExists bool
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT to_regclass($1) IS NOT NULL", "public.customers_status_idx").Scan(&indexExists))
	require.True(t, indexExists, "customers_status_idx must exist")
}

func TestMigrate_SchemaConstraints(t *testing.T) {
	ctx := context.Background()
	pool := startCustomerPostgres(t, ctx)
	require.NoError(t, migrate.Up(ctx, pool, migrations.FS, "."))

	_, err := pool.Exec(ctx, `INSERT INTO customers (id, user_id, status, created_at, updated_at)
		VALUES (gen_random_uuid(), gen_random_uuid(), 'NEW', now(), now())`)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `INSERT INTO customers (id, user_id, status, created_at, updated_at)
		SELECT gen_random_uuid(), user_id, 'NEW', now(), now() FROM customers LIMIT 1`)
	require.Error(t, err, "a duplicate user_id must be rejected (one profile per user)")

	_, err = pool.Exec(ctx, `INSERT INTO customers (id, user_id, status, created_at, updated_at)
		VALUES (gen_random_uuid(), gen_random_uuid(), 'ARCHIVED', now(), now())`)
	require.Error(t, err, "an unknown status must be rejected by the CHECK constraint")

	var headers string
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO outbox (id, topic, event_type, payload, occurred_at)
		VALUES (gen_random_uuid(), 'customer.events', 'customer.profile_created', '\x00', now())
		RETURNING headers::text`).Scan(&headers))
	require.JSONEq(t, `{}`, headers)

	_, err = pool.Exec(ctx, `INSERT INTO idempotency_keys (scope, key, request_hash, created_at)
		VALUES ('customer.update_profile:c', 'k', '\x01', now())`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO idempotency_keys (scope, key, request_hash, created_at)
		VALUES ('customer.update_profile:c', 'k', '\x01', now())`)
	require.Error(t, err, "duplicate (scope, key) must be rejected")

	_, err = pool.Exec(ctx, `INSERT INTO processed_events (event_id, event_type, processed_at)
		VALUES (gen_random_uuid(), 'auth.user_registered', now())`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO processed_events (event_id, event_type, processed_at)
		SELECT event_id, event_type, now() FROM processed_events LIMIT 1`)
	require.Error(t, err, "a duplicate event_id must be rejected (dedup)")
}

func TestMigrate_IdempotencyKeyLengthCheck(t *testing.T) {
	ctx := context.Background()
	pool := startCustomerPostgres(t, ctx)
	require.NoError(t, migrate.Up(ctx, pool, migrations.FS, "."))

	insert := func(key string) error {
		_, err := pool.Exec(ctx, `INSERT INTO idempotency_keys (scope, key, request_hash, created_at)
			VALUES ('customer.update_profile:c', $1, '\x01', now())`, key)
		return err
	}

	require.NoError(t, insert("k"), "a 1-character key is valid")
	require.NoError(t, insert(strings.Repeat("k", 255)), "255 characters is the allowed maximum")
	require.Error(t, insert(""), "an empty key must be rejected")
	require.Error(t, insert(strings.Repeat("k", 256)), "more than 255 characters must be rejected")
}
