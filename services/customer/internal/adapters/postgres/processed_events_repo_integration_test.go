//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/adapters/postgres"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
)

func TestProcessedEventsRepo_MarkOnce(t *testing.T) {
	ctx := context.Background()
	pool := startCustomerPostgres(t, ctx)
	repo := postgres.NewProcessedEventsRepo(pool)

	eventID := id.UUIDv7{}.New()
	first, err := repo.MarkProcessed(ctx, eventID, "auth.user_registered", testNow())
	require.NoError(t, err)
	require.True(t, first)

	second, err := repo.MarkProcessed(ctx, eventID, "auth.user_registered", testNow())
	require.NoError(t, err)
	require.False(t, second)
}

func TestProcessedEventsRepo_RollbackLeavesNoMarker(t *testing.T) {
	ctx := context.Background()
	pool := startCustomerPostgres(t, ctx)
	tx := postgres.NewTransactor(pool)
	repo := postgres.NewProcessedEventsRepo(pool)

	eventID := id.UUIDv7{}.New()
	boom := errors.New("boom")
	require.ErrorIs(t, tx.WithinTx(ctx, func(ctx context.Context, uow domain.UnitOfWork) error {
		if _, err := uow.ProcessedEvents().MarkProcessed(ctx, eventID, "auth.user_registered", testNow()); err != nil {
			return err
		}
		return boom
	}), boom)

	inserted, err := repo.MarkProcessed(ctx, eventID, "auth.user_registered", testNow())
	require.NoError(t, err)
	require.True(t, inserted, "a rolled back marker must not dedupe the retry")
}
