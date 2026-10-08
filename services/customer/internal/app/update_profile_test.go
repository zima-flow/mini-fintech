package app_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/clock"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/events"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/app"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
)

func newUpdateProfileHarness(t *testing.T) (*app.UpdateProfileUseCase, *fakeCustomerRepo, *fakeOutbox, *fakeIdempotencyRepo) {
	t.Helper()
	return newUpdateProfileHarnessWithMetrics(t, nil)
}

func newUpdateProfileHarnessWithMetrics(t *testing.T, metrics domain.Metrics) (*app.UpdateProfileUseCase, *fakeCustomerRepo, *fakeOutbox, *fakeIdempotencyRepo) {
	t.Helper()

	customers := newFakeCustomerRepo()
	outbox := &fakeOutbox{}
	idempotency := newFakeIdempotencyRepo()
	uow := &fakeUnitOfWork{
		customers:       customers,
		idempotency:     idempotency,
		outbox:          outbox,
		processedEvents: newFakeProcessedEventsRepo(),
	}

	uc := app.NewUpdateProfileUseCase(&fakeTransactor{uow: uow}, clock.Fixed{T: fixedNow()}, &sequentialID{}, metrics)
	return uc, customers, outbox, idempotency
}

func TestUpdateProfile_CountsOnlyFirstFill(t *testing.T) {
	t.Parallel()

	metrics := &fakeMetrics{}
	uc, customers, _, _ := newUpdateProfileHarnessWithMetrics(t, metrics)
	seedCustomer(t, customers, "cust-1", "user-1", domain.StatusNew)

	cmd := updateProfileCommand()
	_, err := uc.UpdateProfile(context.Background(), cmd)
	require.NoError(t, err)
	require.Equal(t, 1, metrics.filled)

	cmd.IdempotencyKey = "idem-2"
	_, err = uc.UpdateProfile(context.Background(), cmd)
	require.NoError(t, err)
	require.Equal(t, 1, metrics.filled, "a later edit must not re-count")
}

func seedCustomer(t *testing.T, customers *fakeCustomerRepo, id, userID string, status domain.CustomerStatus) {
	t.Helper()

	require.NoError(t, customers.Create(context.Background(), domain.Customer{
		ID:        id,
		UserID:    userID,
		Status:    status,
		CreatedAt: fixedNow(),
		UpdatedAt: fixedNow(),
	}))
}

func updateProfileCommand() app.UpdateProfileCommand {
	return app.UpdateProfileCommand{
		UserID:         "user-1",
		FullName:       "Ada Lovelace",
		DateOfBirth:    "1990-01-02",
		Address:        "1 Analytical Engine Way",
		Phone:          "+15551234567",
		Citizenship:    "GB",
		IdempotencyKey: "idem-1",
	}
}

func TestUpdateProfile_FirstCompleteUpdate_TransitionsAndEmitsOnce(t *testing.T) {
	t.Parallel()

	uc, customers, outbox, _ := newUpdateProfileHarness(t)
	seedCustomer(t, customers, "cust-1", "user-1", domain.StatusNew)

	res, err := uc.UpdateProfile(context.Background(), updateProfileCommand())
	require.NoError(t, err)

	require.Equal(t, "cust-1", res.Customer.ID)
	require.Equal(t, domain.StatusProfileFilled, res.Customer.Status)
	require.Equal(t, "Ada Lovelace", res.Customer.FullName)
	require.Equal(t, "1990-01-02", res.Customer.DateOfBirth)
	require.Equal(t, "1 Analytical Engine Way", res.Customer.Address)
	require.Equal(t, "+15551234567", res.Customer.Phone)
	require.Equal(t, "GB", res.Customer.Citizenship)
	require.Equal(t, fixedNow(), res.Customer.UpdatedAt, "time must come only from the injected Clock")

	stored := customers.byID["cust-1"]
	require.Equal(t, domain.StatusProfileFilled, stored.Status)
	require.Equal(t, "Ada Lovelace", stored.FullName)

	require.Len(t, outbox.messages, 1, "the first complete update emits exactly one event")
	msg := outbox.messages[0]
	require.Equal(t, events.TopicCustomer, msg.Topic)
	require.Equal(t, events.TypeProfileFilled, msg.EventType)
	require.Equal(t, fixedNow(), msg.OccurredAt)

	env, err := events.Decode(msg.Payload)
	require.NoError(t, err)
	require.Equal(t, events.TypeProfileFilled, env.GetEventType())
	require.Equal(t, msg.ID, env.GetEventId(), "the outbox row id is the event id")

	payload := env.GetProfileFilled()
	require.NotNil(t, payload)
	require.Equal(t, "cust-1", payload.GetCustomerId())
	require.Equal(t, "user-1", payload.GetUserId())
}

func TestUpdateProfile_CarriesTraceHeadersToOutbox(t *testing.T) {
	t.Parallel()

	uc, customers, outbox, _ := newUpdateProfileHarness(t)
	seedCustomer(t, customers, "cust-1", "user-1", domain.StatusNew)

	cmd := updateProfileCommand()
	cmd.Headers = map[string]string{"traceparent": testTraceparent}
	_, err := uc.UpdateProfile(context.Background(), cmd)
	require.NoError(t, err)

	require.Len(t, outbox.messages, 1)
	require.Equal(t, testTraceparent, outbox.messages[0].Headers["traceparent"])
}

func TestUpdateProfile_SecondEdit_UpdatesWithoutEmitting(t *testing.T) {
	t.Parallel()

	uc, customers, outbox, _ := newUpdateProfileHarness(t)
	seedCustomer(t, customers, "cust-1", "user-1", domain.StatusNew)

	first := updateProfileCommand()
	_, err := uc.UpdateProfile(context.Background(), first)
	require.NoError(t, err)

	second := first
	second.IdempotencyKey = "idem-2"
	second.Address = "2 Difference Engine Road"
	res, err := uc.UpdateProfile(context.Background(), second)
	require.NoError(t, err)

	require.Equal(t, "2 Difference Engine Road", res.Customer.Address)
	require.Equal(t, domain.StatusProfileFilled, res.Customer.Status)
	require.Equal(t, "2 Difference Engine Road", customers.byID["cust-1"].Address)
	require.Len(t, outbox.messages, 1, "a later edit must not re-emit profile_filled")
}

func TestUpdateProfile_MissingOrInvalidInput_InvalidArgument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mut  func(*app.UpdateProfileCommand)
	}{
		{name: "missing user id", mut: func(c *app.UpdateProfileCommand) { c.UserID = "" }},
		{name: "missing idempotency key", mut: func(c *app.UpdateProfileCommand) { c.IdempotencyKey = "" }},
		{name: "missing full name", mut: func(c *app.UpdateProfileCommand) { c.FullName = "   " }},
		{name: "missing date of birth", mut: func(c *app.UpdateProfileCommand) { c.DateOfBirth = "" }},
		{name: "malformed date of birth", mut: func(c *app.UpdateProfileCommand) { c.DateOfBirth = "02/01/1990" }},
		{name: "future date of birth", mut: func(c *app.UpdateProfileCommand) { c.DateOfBirth = "2001-02-04" }},
		{name: "missing address", mut: func(c *app.UpdateProfileCommand) { c.Address = "" }},
		{name: "missing phone", mut: func(c *app.UpdateProfileCommand) { c.Phone = "" }},
		{name: "missing citizenship", mut: func(c *app.UpdateProfileCommand) { c.Citizenship = "" }},
		{name: "malformed citizenship", mut: func(c *app.UpdateProfileCommand) { c.Citizenship = "GBR" }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			uc, customers, outbox, idempotency := newUpdateProfileHarness(t)
			seedCustomer(t, customers, "cust-1", "user-1", domain.StatusNew)

			cmd := updateProfileCommand()
			tc.mut(&cmd)

			_, err := uc.UpdateProfile(context.Background(), cmd)
			require.ErrorIs(t, err, errs.ErrInvalidArgument)
			require.Empty(t, outbox.messages)
			require.Empty(t, idempotency.records)
			require.Equal(t, domain.StatusNew, customers.byID["cust-1"].Status)
		})
	}
}

func TestUpdateProfile_LockedStatus_FailedPrecondition(t *testing.T) {
	t.Parallel()

	locked := []domain.CustomerStatus{
		domain.StatusOnKYC,
		domain.StatusActive,
		domain.StatusRejected,
		domain.StatusBlocked,
	}

	for _, status := range locked {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()

			uc, customers, outbox, idempotency := newUpdateProfileHarness(t)
			seedCustomer(t, customers, "cust-1", "user-1", status)

			_, err := uc.UpdateProfile(context.Background(), updateProfileCommand())
			require.ErrorIs(t, err, errs.ErrFailedPrecondition)
			require.Empty(t, outbox.messages)
			require.Empty(t, idempotency.records)
			require.Equal(t, status, customers.byID["cust-1"].Status)
		})
	}
}

func TestUpdateProfile_UnknownProfile_NotFound(t *testing.T) {
	t.Parallel()

	uc, _, outbox, _ := newUpdateProfileHarness(t)

	_, err := uc.UpdateProfile(context.Background(), updateProfileCommand())
	require.ErrorIs(t, err, errs.ErrNotFound)
	require.Empty(t, outbox.messages)
}

func TestUpdateProfile_Replay_ReturnsStoredResponse(t *testing.T) {
	t.Parallel()

	uc, customers, outbox, _ := newUpdateProfileHarness(t)
	seedCustomer(t, customers, "cust-1", "user-1", domain.StatusNew)

	cmd := updateProfileCommand()
	first, err := uc.UpdateProfile(context.Background(), cmd)
	require.NoError(t, err)

	second, err := uc.UpdateProfile(context.Background(), cmd)
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.Len(t, outbox.messages, 1, "a replay must not enqueue a second event")
}

func TestUpdateProfile_ReplayAfterLocked_ReturnsStoredResponse(t *testing.T) {
	t.Parallel()

	uc, customers, outbox, _ := newUpdateProfileHarness(t)
	seedCustomer(t, customers, "cust-1", "user-1", domain.StatusNew)

	cmd := updateProfileCommand()
	first, err := uc.UpdateProfile(context.Background(), cmd)
	require.NoError(t, err)

	locked := customers.byID["cust-1"]
	locked.Status = domain.StatusOnKYC
	require.NoError(t, customers.Update(context.Background(), locked))

	second, err := uc.UpdateProfile(context.Background(), cmd)
	require.NoError(t, err, "a replay must return the stored response even after the profile locks")
	require.Equal(t, first, second)
	require.Len(t, outbox.messages, 1)
}

func TestUpdateProfile_SameKeyDifferentPayload_Conflict(t *testing.T) {
	t.Parallel()

	uc, customers, outbox, _ := newUpdateProfileHarness(t)
	seedCustomer(t, customers, "cust-1", "user-1", domain.StatusNew)

	first := updateProfileCommand()
	_, err := uc.UpdateProfile(context.Background(), first)
	require.NoError(t, err)

	changed := first
	changed.Address = "3 Elsewhere Street"

	_, err = uc.UpdateProfile(context.Background(), changed)
	require.ErrorIs(t, err, errs.ErrConflict)
	require.Equal(t, "1 Analytical Engine Way", customers.byID["cust-1"].Address)
	require.Len(t, outbox.messages, 1)
}
