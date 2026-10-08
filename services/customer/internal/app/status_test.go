package app_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/app"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
)

func TestGetCustomerStatus_Client_ResolvesOwn(t *testing.T) {
	t.Parallel()

	customers := newFakeCustomerRepo()
	seedCustomer(t, customers, "cust-1", "user-1", domain.StatusProfileFilled)
	seedCustomer(t, customers, "cust-9", "user-9", domain.StatusOnKYC)
	uc := app.NewGetCustomerStatusUseCase(customers)

	res, err := uc.GetCustomerStatus(context.Background(), app.GetCustomerStatusCommand{
		UserID:    "user-1",
		ActorRole: domain.RoleClient,
	})
	require.NoError(t, err)
	require.Equal(t, "cust-1", res.CustomerID)
	require.Equal(t, domain.StatusProfileFilled, res.Status)

	res, err = uc.GetCustomerStatus(context.Background(), app.GetCustomerStatusCommand{
		UserID:     "user-1",
		CustomerID: "cust-9",
		ActorRole:  domain.RoleClient,
	})
	require.NoError(t, err)
	require.Equal(t, "cust-1", res.CustomerID)
	require.Equal(t, domain.StatusProfileFilled, res.Status)
}

func TestGetCustomerStatus_Staff_ResolvesTarget(t *testing.T) {
	t.Parallel()

	for _, role := range []domain.Role{domain.RoleOfficer, domain.RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			customers := newFakeCustomerRepo()
			seedCustomer(t, customers, "cust-1", "user-1", domain.StatusProfileFilled)
			seedCustomer(t, customers, "cust-9", "user-9", domain.StatusOnKYC)
			uc := app.NewGetCustomerStatusUseCase(customers)

			res, err := uc.GetCustomerStatus(context.Background(), app.GetCustomerStatusCommand{
				UserID:     "user-1",
				CustomerID: "cust-9",
				ActorRole:  role,
			})
			require.NoError(t, err)
			require.Equal(t, "cust-9", res.CustomerID)
			require.Equal(t, domain.StatusOnKYC, res.Status)
		})
	}
}

func TestGetCustomerStatus_StaffMissingCustomerID_InvalidArgument(t *testing.T) {
	t.Parallel()

	uc := app.NewGetCustomerStatusUseCase(newFakeCustomerRepo())

	_, err := uc.GetCustomerStatus(context.Background(), app.GetCustomerStatusCommand{
		UserID:    "user-1",
		ActorRole: domain.RoleOfficer,
	})
	require.ErrorIs(t, err, errs.ErrInvalidArgument)
}

func TestGetCustomerStatus_InvalidRole_Denied(t *testing.T) {
	t.Parallel()

	for _, role := range []domain.Role{domain.Role(""), domain.Role("AUDITOR")} {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			customers := newFakeCustomerRepo()
			seedCustomer(t, customers, "cust-1", "user-1", domain.StatusProfileFilled)
			uc := app.NewGetCustomerStatusUseCase(customers)

			_, err := uc.GetCustomerStatus(context.Background(), app.GetCustomerStatusCommand{
				UserID:    "user-1",
				ActorRole: role,
			})
			require.ErrorIs(t, err, errs.ErrPermissionDenied)
		})
	}
}

func TestGetCustomerStatus_Unknown_NotFound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cmd  app.GetCustomerStatusCommand
	}{
		{name: "owner", cmd: app.GetCustomerStatusCommand{UserID: "user-9", ActorRole: domain.RoleClient}},
		{name: "officer", cmd: app.GetCustomerStatusCommand{CustomerID: "cust-9", ActorRole: domain.RoleOfficer}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			uc := app.NewGetCustomerStatusUseCase(newFakeCustomerRepo())

			_, err := uc.GetCustomerStatus(context.Background(), tc.cmd)
			require.ErrorIs(t, err, errs.ErrNotFound)
		})
	}
}
