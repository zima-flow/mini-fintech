package app_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/app"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
)

func seededCustomers(n int, status domain.CustomerStatus) *fakeCustomerRepo {
	customers := newFakeCustomerRepo()
	for i := 1; i <= n; i++ {
		c := domain.Customer{
			ID:        fmt.Sprintf("cust-%d", i),
			UserID:    fmt.Sprintf("user-%d", i),
			Status:    status,
			CreatedAt: fixedNow(),
			UpdatedAt: fixedNow(),
		}
		customers.byUserID[c.UserID] = c
		customers.byID[c.ID] = c
	}
	return customers
}

func TestListCustomers_NonStaff_Denied(t *testing.T) {
	t.Parallel()

	for _, role := range []domain.Role{domain.RoleClient, domain.Role(""), domain.Role("AUDITOR")} {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			customers := seededCustomers(1, domain.StatusNew)
			uc := app.NewListCustomersUseCase(customers)

			_, err := uc.ListCustomers(context.Background(), app.ListCustomersCommand{ActorRole: role})
			require.ErrorIs(t, err, errs.ErrPermissionDenied)
			require.Empty(t, customers.listCalls, "a denied caller must not query the repository")
		})
	}
}

func TestListCustomers_Officer_ListsAllWithPagination(t *testing.T) {
	t.Parallel()

	customers := seededCustomers(5, domain.StatusNew)
	uc := app.NewListCustomersUseCase(customers)

	page1, err := uc.ListCustomers(context.Background(), app.ListCustomersCommand{
		ActorRole: domain.RoleOfficer,
		PageSize:  2,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"cust-1", "cust-2"}, customerIDs(page1.Customers))
	require.NotEmpty(t, page1.NextPageToken)

	page2, err := uc.ListCustomers(context.Background(), app.ListCustomersCommand{
		ActorRole: domain.RoleOfficer,
		PageSize:  2,
		PageToken: page1.NextPageToken,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"cust-3", "cust-4"}, customerIDs(page2.Customers))
	require.NotEmpty(t, page2.NextPageToken)

	page3, err := uc.ListCustomers(context.Background(), app.ListCustomersCommand{
		ActorRole: domain.RoleOfficer,
		PageSize:  2,
		PageToken: page2.NextPageToken,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"cust-5"}, customerIDs(page3.Customers))
	require.Empty(t, page3.NextPageToken, "the last page has no cursor")
}

func TestListCustomers_FilterByStatus(t *testing.T) {
	t.Parallel()

	customers := newFakeCustomerRepo()
	seedCustomer(t, customers, "cust-1", "user-1", domain.StatusNew)
	seedCustomer(t, customers, "cust-2", "user-2", domain.StatusProfileFilled)
	seedCustomer(t, customers, "cust-3", "user-3", domain.StatusProfileFilled)
	uc := app.NewListCustomersUseCase(customers)

	res, err := uc.ListCustomers(context.Background(), app.ListCustomersCommand{
		ActorRole: domain.RoleOfficer,
		Status:    domain.StatusProfileFilled,
	})
	require.NoError(t, err)
	require.Len(t, res.Customers, 2)
	for _, c := range res.Customers {
		require.Equal(t, domain.StatusProfileFilled, c.Status)
	}
}

func TestListCustomers_InvalidPageToken_InvalidArgument(t *testing.T) {
	t.Parallel()

	uc := app.NewListCustomersUseCase(seededCustomers(1, domain.StatusNew))

	_, err := uc.ListCustomers(context.Background(), app.ListCustomersCommand{
		ActorRole: domain.RoleOfficer,
		PageToken: "!!!not-base64!!!",
	})
	require.ErrorIs(t, err, errs.ErrInvalidArgument)
}

func TestListCustomers_InvalidInput_InvalidArgument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cmd  app.ListCustomersCommand
	}{
		{name: "negative page size", cmd: app.ListCustomersCommand{ActorRole: domain.RoleOfficer, PageSize: -1}},
		{name: "unknown status", cmd: app.ListCustomersCommand{ActorRole: domain.RoleOfficer, Status: "ARCHIVED"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			uc := app.NewListCustomersUseCase(seededCustomers(1, domain.StatusNew))

			_, err := uc.ListCustomers(context.Background(), tc.cmd)
			require.ErrorIs(t, err, errs.ErrInvalidArgument)
		})
	}
}

func TestListCustomers_PageSizeClamping(t *testing.T) {
	t.Parallel()

	customers := seededCustomers(3, domain.StatusNew)
	uc := app.NewListCustomersUseCase(customers)

	_, err := uc.ListCustomers(context.Background(), app.ListCustomersCommand{
		ActorRole: domain.RoleOfficer,
		PageSize:  0,
	})
	require.NoError(t, err)
	require.Equal(t, 21, customers.listCalls[0].Limit, "default page size is 20 (+1 probe)")

	_, err = uc.ListCustomers(context.Background(), app.ListCustomersCommand{
		ActorRole: domain.RoleOfficer,
		PageSize:  1000,
	})
	require.NoError(t, err)
	require.Equal(t, 101, customers.listCalls[1].Limit, "page size is capped at 100 (+1 probe)")
}

func customerIDs(customers []domain.Customer) []string {
	ids := make([]string, len(customers))
	for i, c := range customers {
		ids[i] = c.ID
	}
	return ids
}
