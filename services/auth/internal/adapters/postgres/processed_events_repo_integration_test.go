//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/clock"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/events"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/adapters/postgres"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/app"
)

func TestProcessedEventsRepo_MarkProcessed(t *testing.T) {
	ctx := context.Background()
	pool := startAuthPostgres(t, ctx)
	repo := postgres.NewProcessedEventsRepo(pool)

	eventID := id.UUIDv7{}.New()
	inserted, err := repo.MarkProcessed(ctx, eventID, events.TypeProfileCreated, testNow())
	require.NoError(t, err)
	require.True(t, inserted)

	inserted, err = repo.MarkProcessed(ctx, eventID, events.TypeProfileCreated, testNow())
	require.NoError(t, err)
	require.False(t, inserted, "a duplicate event id must not be recorded twice")
}

func TestLinkCustomer_LinksOnceAndRollsBackOnFailure(t *testing.T) {
	ctx := context.Background()
	pool := startAuthPostgres(t, ctx)
	uc := app.NewLinkCustomerUseCase(postgres.NewTransactor(pool), clock.Fixed{T: testNow()})
	users := postgres.NewUserRepo(pool)

	user := seedUser(t, pool)
	cmd := app.LinkCustomerCommand{
		EventID:    id.UUIDv7{}.New(),
		EventType:  events.TypeProfileCreated,
		UserID:     user.ID,
		CustomerID: id.UUIDv7{}.New(),
	}
	require.NoError(t, uc.LinkCustomer(ctx, cmd))

	got, err := users.ByID(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, cmd.CustomerID, got.CustomerID)

	duplicate := cmd
	duplicate.CustomerID = id.UUIDv7{}.New()
	require.NoError(t, uc.LinkCustomer(ctx, duplicate))
	got, err = users.ByID(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, cmd.CustomerID, got.CustomerID, "a duplicate event must not relink")

	other := seedUser(t, pool)
	failing := app.LinkCustomerCommand{
		EventID:    id.UUIDv7{}.New(),
		EventType:  events.TypeProfileCreated,
		UserID:     id.UUIDv7{}.New(),
		CustomerID: id.UUIDv7{}.New(),
	}
	require.ErrorIs(t, uc.LinkCustomer(ctx, failing), errs.ErrNotFound)

	retry := failing
	retry.UserID = other.ID
	require.NoError(t, uc.LinkCustomer(ctx, retry))
	got, err = users.ByID(ctx, other.ID)
	require.NoError(t, err)
	require.Equal(t, retry.CustomerID, got.CustomerID)
}
