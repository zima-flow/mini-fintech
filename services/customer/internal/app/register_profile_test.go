package app_test

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/clock"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/events"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/app"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
)

func fixedNow() time.Time {
	return time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
}

const testTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

func newRegisterProfileHarness(t *testing.T) (*app.RegisterProfileUseCase, *fakeCustomerRepo, *fakeOutbox, *fakeProcessedEventsRepo) {
	t.Helper()
	return newRegisterProfileHarnessWithMetrics(t, nil)
}

func newRegisterProfileHarnessWithMetrics(t *testing.T, metrics domain.Metrics) (*app.RegisterProfileUseCase, *fakeCustomerRepo, *fakeOutbox, *fakeProcessedEventsRepo) {
	t.Helper()

	customers := newFakeCustomerRepo()
	outbox := &fakeOutbox{}
	processed := newFakeProcessedEventsRepo()
	uow := &fakeUnitOfWork{
		customers:       customers,
		idempotency:     newFakeIdempotencyRepo(),
		outbox:          outbox,
		processedEvents: processed,
	}

	uc := app.NewRegisterProfileUseCase(&fakeTransactor{uow: uow}, clock.Fixed{T: fixedNow()}, &sequentialID{}, metrics)
	return uc, customers, outbox, processed
}

type fakeMetrics struct {
	created int
	filled  int
}

func (m *fakeMetrics) ProfileCreated(context.Context) { m.created++ }
func (m *fakeMetrics) ProfileFilled(context.Context)  { m.filled++ }

func TestRegisterProfile_CountsOnlyFirstDelivery(t *testing.T) {
	t.Parallel()

	metrics := &fakeMetrics{}
	uc, _, _, _ := newRegisterProfileHarnessWithMetrics(t, metrics)
	cmd := userRegisteredCommand()

	require.NoError(t, uc.RegisterProfile(context.Background(), cmd))
	require.Equal(t, 1, metrics.created)

	require.NoError(t, uc.RegisterProfile(context.Background(), cmd))
	require.Equal(t, 1, metrics.created, "a duplicate delivery must not re-count")
}

func userRegisteredCommand() app.RegisterProfileCommand {
	return app.RegisterProfileCommand{
		EventID:   "evt-1",
		EventType: events.TypeUserRegistered,
		UserID:    "user-1",
	}
}

func TestRegisterProfile_FirstDelivery_CreatesProfileAndEvent(t *testing.T) {
	t.Parallel()

	uc, customers, outbox, processed := newRegisterProfileHarness(t)

	require.NoError(t, uc.RegisterProfile(context.Background(), userRegisteredCommand()))

	require.Equal(t, 1, processed.calls, "the event id is recorded once")

	stored := customers.byUserID["user-1"]
	require.Equal(t, "id-1", stored.ID)
	require.Equal(t, "user-1", stored.UserID)
	require.Equal(t, domain.StatusNew, stored.Status)
	require.Equal(t, fixedNow(), stored.CreatedAt, "time must come only from the injected Clock")
	require.Equal(t, fixedNow(), stored.UpdatedAt)

	require.Len(t, outbox.messages, 1, "exactly one event per new profile")
	msg := outbox.messages[0]
	require.Equal(t, events.TopicCustomer, msg.Topic)
	require.Equal(t, events.TypeProfileCreated, msg.EventType)
	require.Equal(t, fixedNow(), msg.OccurredAt)

	env, err := events.Decode(msg.Payload)
	require.NoError(t, err)
	require.Equal(t, events.TypeProfileCreated, env.GetEventType())
	require.Equal(t, env.GetEventId(), msg.ID, "the outbox row id is the event id")

	payload := env.GetProfileCreated()
	require.NotNil(t, payload)
	require.Equal(t, "user-1", payload.GetUserId())
	require.Equal(t, "id-1", payload.GetCustomerId())
}

func TestRegisterProfile_CarriesTraceHeadersToOutbox(t *testing.T) {
	t.Parallel()

	uc, _, outbox, _ := newRegisterProfileHarness(t)
	cmd := userRegisteredCommand()
	cmd.Headers = map[string]string{"traceparent": testTraceparent}

	require.NoError(t, uc.RegisterProfile(context.Background(), cmd))

	require.Len(t, outbox.messages, 1)
	require.Equal(t, testTraceparent, outbox.messages[0].Headers["traceparent"])
}

func TestRegisterProfile_Duplicate_NoOp(t *testing.T) {
	t.Parallel()

	uc, customers, outbox, _ := newRegisterProfileHarness(t)
	cmd := userRegisteredCommand()

	require.NoError(t, uc.RegisterProfile(context.Background(), cmd))
	require.NoError(t, uc.RegisterProfile(context.Background(), cmd))

	require.Len(t, customers.byUserID, 1, "a duplicate event must not create a second profile")
	require.Len(t, outbox.messages, 1, "a duplicate event must not double-publish")
}

func TestRegisterProfile_InvalidCommand_Rejected(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cmd  app.RegisterProfileCommand
	}{
		{name: "missing event id", cmd: app.RegisterProfileCommand{EventType: events.TypeUserRegistered, UserID: "user-1"}},
		{name: "missing event type", cmd: app.RegisterProfileCommand{EventID: "evt-1", UserID: "user-1"}},
		{name: "missing user id", cmd: app.RegisterProfileCommand{EventID: "evt-1", EventType: events.TypeUserRegistered}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			uc, customers, outbox, processed := newRegisterProfileHarness(t)

			err := uc.RegisterProfile(context.Background(), tc.cmd)
			require.ErrorIs(t, err, errs.ErrInvalidArgument)
			require.Empty(t, customers.byUserID)
			require.Empty(t, outbox.messages)
			require.Zero(t, processed.calls, "an invalid event must not touch the dedup store")
		})
	}
}

type fakeCustomerRepo struct {
	byUserID  map[string]domain.Customer
	byID      map[string]domain.Customer
	listCalls []domain.CustomerFilter
}

func newFakeCustomerRepo() *fakeCustomerRepo {
	return &fakeCustomerRepo{
		byUserID: map[string]domain.Customer{},
		byID:     map[string]domain.Customer{},
	}
}

func (r *fakeCustomerRepo) Create(_ context.Context, c domain.Customer) error {
	if _, ok := r.byUserID[c.UserID]; ok {
		return errs.ErrAlreadyExists
	}
	r.byUserID[c.UserID] = c
	r.byID[c.ID] = c
	return nil
}

func (r *fakeCustomerRepo) ByUserID(_ context.Context, userID string) (domain.Customer, error) {
	c, ok := r.byUserID[userID]
	if !ok {
		return domain.Customer{}, errs.ErrNotFound
	}
	return c, nil
}

func (r *fakeCustomerRepo) ByID(_ context.Context, customerID string) (domain.Customer, error) {
	c, ok := r.byID[customerID]
	if !ok {
		return domain.Customer{}, errs.ErrNotFound
	}
	return c, nil
}

func (r *fakeCustomerRepo) Update(_ context.Context, c domain.Customer) error {
	if _, ok := r.byID[c.ID]; !ok {
		return errs.ErrNotFound
	}
	r.byUserID[c.UserID] = c
	r.byID[c.ID] = c
	return nil
}

func (r *fakeCustomerRepo) List(_ context.Context, filter domain.CustomerFilter) ([]domain.Customer, error) {
	r.listCalls = append(r.listCalls, filter)

	all := make([]domain.Customer, 0, len(r.byID))
	for _, c := range r.byID {
		if filter.Status != "" && c.Status != filter.Status {
			continue
		}
		all = append(all, c)
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].CreatedAt.Before(all[j].CreatedAt)
		}
		return all[i].ID < all[j].ID
	})

	if filter.Offset >= len(all) {
		return nil, nil
	}
	end := filter.Offset + filter.Limit
	if end > len(all) {
		end = len(all)
	}
	return all[filter.Offset:end], nil
}

type fakeIdempotencyRepo struct {
	records map[string]domain.IdempotencyRecord
}

func newFakeIdempotencyRepo() *fakeIdempotencyRepo {
	return &fakeIdempotencyRepo{records: map[string]domain.IdempotencyRecord{}}
}

func recordKey(scope, key string) string { return scope + "|" + key }

func (r *fakeIdempotencyRepo) Get(_ context.Context, scope, key string) (domain.IdempotencyRecord, error) {
	rec, ok := r.records[recordKey(scope, key)]
	if !ok {
		return domain.IdempotencyRecord{}, errs.ErrNotFound
	}
	return rec, nil
}

func (r *fakeIdempotencyRepo) Put(_ context.Context, rec domain.IdempotencyRecord) error {
	if _, ok := r.records[recordKey(rec.Scope, rec.Key)]; ok {
		return errs.ErrAlreadyExists
	}
	r.records[recordKey(rec.Scope, rec.Key)] = rec
	return nil
}

type fakeOutbox struct {
	messages []domain.OutboxMessage
}

func (o *fakeOutbox) Enqueue(_ context.Context, msg domain.OutboxMessage) error {
	o.messages = append(o.messages, msg)
	return nil
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

type fakeUnitOfWork struct {
	customers       *fakeCustomerRepo
	idempotency     *fakeIdempotencyRepo
	outbox          *fakeOutbox
	processedEvents *fakeProcessedEventsRepo
}

func (u *fakeUnitOfWork) Customers() domain.CustomerRepo { return u.customers }
func (u *fakeUnitOfWork) Idempotency() domain.IdempotencyRepo {
	return u.idempotency
}
func (u *fakeUnitOfWork) Outbox() domain.Outbox { return u.outbox }
func (u *fakeUnitOfWork) ProcessedEvents() domain.ProcessedEventsRepo {
	return u.processedEvents
}

type fakeTransactor struct{ uow *fakeUnitOfWork }

func (t *fakeTransactor) WithinTx(ctx context.Context, fn func(context.Context, domain.UnitOfWork) error) error {
	return fn(ctx, t.uow)
}

type sequentialID struct{ n int }

func (s *sequentialID) New() string {
	s.n++
	return fmt.Sprintf("id-%d", s.n)
}
