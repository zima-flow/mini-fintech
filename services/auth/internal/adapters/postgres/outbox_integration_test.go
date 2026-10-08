//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/adapters/postgres"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

func TestOutbox_EnqueueJoinsTransaction(t *testing.T) {
	ctx := context.Background()
	pool := startAuthPostgres(t, ctx)
	tx := postgres.NewTransactor(pool)

	msg := domain.OutboxMessage{
		ID:         id.UUIDv7{}.New(),
		Topic:      "auth.events",
		EventType:  "auth.user_registered",
		Headers:    map[string]string{"traceparent": "00-trace-span-01", "x-request-id": "req-1"},
		Payload:    []byte("encoded-envelope"),
		OccurredAt: testNow(),
	}
	require.NoError(t, tx.WithinTx(ctx, func(ctx context.Context, uow domain.UnitOfWork) error {
		return uow.Outbox().Enqueue(ctx, msg)
	}))

	var (
		gotID        string
		gotTopic     string
		gotEventType string
		headers      map[string]string
		payload      []byte
		occurredAt   pgtype.Timestamptz
		publishedAt  pgtype.Timestamptz
	)
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id::text, topic, event_type, headers, payload, occurred_at, published_at FROM outbox WHERE id = $1`,
		msg.ID,
	).Scan(&gotID, &gotTopic, &gotEventType, &headers, &payload, &occurredAt, &publishedAt))
	require.Equal(t, msg.ID, gotID)
	require.Equal(t, msg.Topic, gotTopic)
	require.Equal(t, msg.EventType, gotEventType)
	require.Equal(t, msg.Headers, headers)
	require.Equal(t, msg.Payload, payload)
	require.True(t, occurredAt.Valid)
	require.Equal(t, msg.OccurredAt, occurredAt.Time.UTC())
	require.False(t, publishedAt.Valid, "a fresh row is unpublished")

	rolledBack := msg
	rolledBack.ID = id.UUIDv7{}.New()
	boom := errors.New("boom")
	require.ErrorIs(t, tx.WithinTx(ctx, func(ctx context.Context, uow domain.UnitOfWork) error {
		if err := uow.Outbox().Enqueue(ctx, rolledBack); err != nil {
			return err
		}
		return boom
	}), boom)

	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE id = $1`, rolledBack.ID).Scan(&count))
	require.Zero(t, count, "a rolled back enqueue must not persist")
}
