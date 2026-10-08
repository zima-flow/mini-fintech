package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/clock"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/events"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/app"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

func linkHarness(t *testing.T) (*app.LinkCustomerUseCase, *fakeUserRepo, *fakeProcessedEventsRepo) {
	t.Helper()

	users := newFakeUserRepo()
	users.byID["user-1"] = domain.User{
		ID: "user-1", Email: "user@example.com", Role: domain.RoleClient,
	}
	users.byEmail["user@example.com"] = users.byID["user-1"]

	processed := newFakeProcessedEventsRepo()
	uow := &fakeUnitOfWork{users: users, processedEvents: processed}
	uc := app.NewLinkCustomerUseCase(&fakeTransactor{uow: uow}, clock.Fixed{T: fixedNow()})
	return uc, users, processed
}

func linkCommand() app.LinkCustomerCommand {
	return app.LinkCustomerCommand{
		EventID:    "evt-1",
		EventType:  events.TypeProfileCreated,
		UserID:     "user-1",
		CustomerID: "cust-1",
	}
}

func TestLinkCustomer_FirstDelivery_LinksUser(t *testing.T) {
	t.Parallel()

	uc, users, processed := linkHarness(t)

	require.NoError(t, uc.LinkCustomer(context.Background(), linkCommand()))

	require.Equal(t, 1, processed.calls)
	require.Len(t, users.links, 1)
	require.Equal(t, "user-1", users.links[0].userID)
	require.Equal(t, "cust-1", users.links[0].customerID)
	require.Equal(t, fixedNow(), users.links[0].at.UTC())
	require.Equal(t, "cust-1", users.byID["user-1"].CustomerID)
}

func TestLinkCustomer_Duplicate_NoOp(t *testing.T) {
	t.Parallel()

	uc, users, _ := linkHarness(t)
	cmd := linkCommand()

	require.NoError(t, uc.LinkCustomer(context.Background(), cmd))
	require.NoError(t, uc.LinkCustomer(context.Background(), cmd))

	require.Len(t, users.links, 1, "the second delivery must not link again")
}

func TestLinkCustomer_UnknownUser_PropagatesNotFound(t *testing.T) {
	t.Parallel()

	uc, _, _ := linkHarness(t)
	cmd := linkCommand()
	cmd.UserID = "missing"

	err := uc.LinkCustomer(context.Background(), cmd)
	require.ErrorIs(t, err, errs.ErrNotFound)
}

func TestLinkCustomer_InvalidCommand_Rejected(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cmd  app.LinkCustomerCommand
	}{
		{name: "missing event id", cmd: app.LinkCustomerCommand{EventType: events.TypeProfileCreated, UserID: "user-1", CustomerID: "cust-1"}},
		{name: "missing event type", cmd: app.LinkCustomerCommand{EventID: "evt-1", UserID: "user-1", CustomerID: "cust-1"}},
		{name: "missing user id", cmd: app.LinkCustomerCommand{EventID: "evt-1", EventType: events.TypeProfileCreated, CustomerID: "cust-1"}},
		{name: "missing customer id", cmd: app.LinkCustomerCommand{EventID: "evt-1", EventType: events.TypeProfileCreated, UserID: "user-1"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			uc, users, processed := linkHarness(t)
			err := uc.LinkCustomer(context.Background(), tc.cmd)
			require.ErrorIs(t, err, errs.ErrInvalidArgument)
			require.Empty(t, users.links)
			require.Zero(t, processed.calls)
		})
	}
}

// --- fakes

type linkCall struct {
	userID     string
	customerID string
	at         time.Time
}

type fakeProcessedEventsRepo struct {
	seen  map[string]bool
	calls int
}

func newFakeProcessedEventsRepo() *fakeProcessedEventsRepo {
	return &fakeProcessedEventsRepo{seen: map[string]bool{}}
}

func (r *fakeProcessedEventsRepo) MarkProcessed(_ context.Context, eventID, _ string, _ time.Time) (bool, error) {
	r.calls++
	if r.seen[eventID] {
		return false, nil
	}
	r.seen[eventID] = true
	return true, nil
}
