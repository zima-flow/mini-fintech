package app_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/app"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
)

func TestGetCustomer_StaffRole_ReturnsCustomer(t *testing.T) {
	t.Parallel()

	for _, role := range []domain.Role{domain.RoleOfficer, domain.RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			customers := newFakeCustomerRepo()
			seedCustomer(t, customers, "cust-2", "user-2", domain.StatusActive)
			uc := app.NewGetCustomerUseCase(customers)

			res, err := uc.GetCustomer(context.Background(), app.GetCustomerCommand{
				CustomerID: "cust-2",
				ActorRole:  role,
			})
			require.NoError(t, err)
			require.Equal(t, "cust-2", res.Customer.ID)
			require.Equal(t, domain.StatusActive, res.Customer.Status)
		})
	}
}

func TestGetCustomer_NonStaff_Denied(t *testing.T) {
	t.Parallel()

	for _, role := range []domain.Role{domain.RoleClient, domain.Role(""), domain.Role("AUDITOR")} {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			customers := newFakeCustomerRepo()
			seedCustomer(t, customers, "cust-2", "user-2", domain.StatusActive)
			uc := app.NewGetCustomerUseCase(customers)

			_, err := uc.GetCustomer(context.Background(), app.GetCustomerCommand{
				CustomerID: "does-not-exist",
				ActorRole:  role,
			})
			require.ErrorIs(t, err, errs.ErrPermissionDenied)
		})
	}
}

func TestGetCustomer_StaffMissingCustomerID_InvalidArgument(t *testing.T) {
	t.Parallel()

	uc := app.NewGetCustomerUseCase(newFakeCustomerRepo())

	_, err := uc.GetCustomer(context.Background(), app.GetCustomerCommand{ActorRole: domain.RoleOfficer})
	require.ErrorIs(t, err, errs.ErrInvalidArgument)
}

func TestGetCustomer_Unknown_NotFound(t *testing.T) {
	t.Parallel()

	uc := app.NewGetCustomerUseCase(newFakeCustomerRepo())

	_, err := uc.GetCustomer(context.Background(), app.GetCustomerCommand{
		CustomerID: "cust-9",
		ActorRole:  domain.RoleOfficer,
	})
	require.ErrorIs(t, err, errs.ErrNotFound)
}
