//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/adapters/postgres"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
)

func TestCustomerRepo_CreateFindUpdate(t *testing.T) {
	ctx := context.Background()
	pool := startCustomerPostgres(t, ctx)
	repo := postgres.NewCustomerRepo(pool)
	customer := seedCustomer(t, pool, domain.StatusNew, testNow())

	byUserID, err := repo.ByUserID(ctx, customer.UserID)
	require.NoError(t, err)
	require.Equal(t, customer, byUserID)
	require.Empty(t, byUserID.DateOfBirth, "date_of_birth is NULL until the first update")
	require.Empty(t, byUserID.Citizenship, "citizenship is NULL until the first update")

	byID, err := repo.ByID(ctx, customer.ID)
	require.NoError(t, err)
	require.Equal(t, customer, byID)

	filled := customer
	filled.FullName = "Ada Lovelace"
	filled.DateOfBirth = "1815-12-10"
	filled.Address = "12 Analytical Engine Way"
	filled.Phone = "+15550100"
	filled.Citizenship = "GB"
	filled.Status = domain.StatusProfileFilled
	filled.UpdatedAt = testNow().Add(time.Hour)
	require.NoError(t, repo.Update(ctx, filled))

	got, err := repo.ByID(ctx, customer.ID)
	require.NoError(t, err)
	require.Equal(t, filled, got)
}

func TestCustomerRepo_DuplicateUserID_AlreadyExists(t *testing.T) {
	ctx := context.Background()
	pool := startCustomerPostgres(t, ctx)
	repo := postgres.NewCustomerRepo(pool)
	customer := seedCustomer(t, pool, domain.StatusNew, testNow())

	duplicate := customer
	duplicate.ID = id.UUIDv7{}.New()
	require.ErrorIs(t, repo.Create(ctx, duplicate), errs.ErrAlreadyExists)
}

func TestCustomerRepo_NotFound(t *testing.T) {
	ctx := context.Background()
	pool := startCustomerPostgres(t, ctx)
	repo := postgres.NewCustomerRepo(pool)

	_, err := repo.ByUserID(ctx, id.UUIDv7{}.New())
	require.ErrorIs(t, err, errs.ErrNotFound)

	_, err = repo.ByID(ctx, id.UUIDv7{}.New())
	require.ErrorIs(t, err, errs.ErrNotFound)

	missing := domain.Customer{
		ID:        id.UUIDv7{}.New(),
		UserID:    id.UUIDv7{}.New(),
		Status:    domain.StatusNew,
		CreatedAt: testNow(),
		UpdatedAt: testNow(),
	}
	require.ErrorIs(t, repo.Update(ctx, missing), errs.ErrNotFound)
}

func TestCustomerRepo_List(t *testing.T) {
	ctx := context.Background()
	pool := startCustomerPostgres(t, ctx)
	repo := postgres.NewCustomerRepo(pool)

	first := seedCustomer(t, pool, domain.StatusNew, testNow())
	second := seedCustomer(t, pool, domain.StatusProfileFilled, testNow().Add(time.Minute))
	third := seedCustomer(t, pool, domain.StatusNew, testNow().Add(2*time.Minute))

	all, err := repo.List(ctx, domain.CustomerFilter{Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{first.ID, second.ID, third.ID}, customerIDs(all))

	onlyNew, err := repo.List(ctx, domain.CustomerFilter{Status: domain.StatusNew, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{first.ID, third.ID}, customerIDs(onlyNew))

	page, err := repo.List(ctx, domain.CustomerFilter{Limit: 2})
	require.NoError(t, err)
	require.Equal(t, []string{first.ID, second.ID}, customerIDs(page))

	next, err := repo.List(ctx, domain.CustomerFilter{Limit: 2, Offset: 2})
	require.NoError(t, err)
	require.Equal(t, []string{third.ID}, customerIDs(next))
}

func customerIDs(customers []domain.Customer) []string {
	ids := make([]string, len(customers))
	for i, c := range customers {
		ids[i] = c.ID
	}
	return ids
}
