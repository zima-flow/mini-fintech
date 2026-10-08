package app_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/app"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
)

func TestGetProfile_Owner_ReturnsOwnProfile(t *testing.T) {
	t.Parallel()

	customers := newFakeCustomerRepo()
	seedCustomer(t, customers, "cust-1", "user-1", domain.StatusProfileFilled)
	uc := app.NewGetProfileUseCase(customers)

	res, err := uc.GetProfile(context.Background(), app.GetProfileCommand{UserID: "user-1"})
	require.NoError(t, err)
	require.Equal(t, "cust-1", res.Customer.ID)
	require.Equal(t, "user-1", res.Customer.UserID)
	require.Equal(t, domain.StatusProfileFilled, res.Customer.Status)
}

func TestGetProfile_UnknownUser_NotFound(t *testing.T) {
	t.Parallel()

	uc := app.NewGetProfileUseCase(newFakeCustomerRepo())

	_, err := uc.GetProfile(context.Background(), app.GetProfileCommand{UserID: "user-1"})
	require.ErrorIs(t, err, errs.ErrNotFound)
}

func TestGetProfile_MissingUserID_InvalidArgument(t *testing.T) {
	t.Parallel()

	uc := app.NewGetProfileUseCase(newFakeCustomerRepo())

	_, err := uc.GetProfile(context.Background(), app.GetProfileCommand{})
	require.ErrorIs(t, err, errs.ErrInvalidArgument)
}
