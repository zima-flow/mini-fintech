//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/adapters/postgres"
)

func TestUserRepo_CreateFindAndLink(t *testing.T) {
	ctx := context.Background()
	pool := startAuthPostgres(t, ctx)
	repo := postgres.NewUserRepo(pool)
	user := seedUser(t, pool)

	byEmail, err := repo.ByEmail(ctx, user.Email)
	require.NoError(t, err)
	require.Equal(t, user, byEmail)

	byID, err := repo.ByID(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, user, byID)
	require.Empty(t, byID.CustomerID, "customer_id is NULL until linked")

	customerID := id.UUIDv7{}.New()
	linkedAt := testNow().Add(time.Hour)
	require.NoError(t, repo.SetCustomerID(ctx, user.ID, customerID, linkedAt))

	linked, err := repo.ByID(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, customerID, linked.CustomerID)
	require.Equal(t, linkedAt, linked.UpdatedAt)
}

func TestUserRepo_DuplicateEmail_AlreadyExists(t *testing.T) {
	ctx := context.Background()
	pool := startAuthPostgres(t, ctx)
	repo := postgres.NewUserRepo(pool)
	user := seedUser(t, pool)

	duplicate := user
	duplicate.ID = id.UUIDv7{}.New()
	require.ErrorIs(t, repo.Create(ctx, duplicate), errs.ErrAlreadyExists)
}

func TestUserRepo_NotFound(t *testing.T) {
	ctx := context.Background()
	pool := startAuthPostgres(t, ctx)
	repo := postgres.NewUserRepo(pool)

	_, err := repo.ByEmail(ctx, "nobody@example.com")
	require.ErrorIs(t, err, errs.ErrNotFound)

	_, err = repo.ByID(ctx, id.UUIDv7{}.New())
	require.ErrorIs(t, err, errs.ErrNotFound)

	err = repo.SetCustomerID(ctx, id.UUIDv7{}.New(), id.UUIDv7{}.New(), testNow())
	require.ErrorIs(t, err, errs.ErrNotFound)
}
