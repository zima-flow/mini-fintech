package app_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/clock"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/events"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/app"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

func fixedNow() time.Time {
	return time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
}

const goodPassword = "correct horse battery staple"

const testTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

type fakeUserRepo struct {
	byEmail map[string]domain.User
	byID    map[string]domain.User
	links   []linkCall
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{
		byEmail: map[string]domain.User{},
		byID:    map[string]domain.User{},
	}
}

func (r *fakeUserRepo) Create(_ context.Context, u domain.User) error {
	if _, ok := r.byEmail[u.Email]; ok {
		return errs.ErrAlreadyExists
	}
	r.byEmail[u.Email] = u
	r.byID[u.ID] = u
	return nil
}

func (r *fakeUserRepo) ByEmail(_ context.Context, email string) (domain.User, error) {
	u, ok := r.byEmail[email]
	if !ok {
		return domain.User{}, errs.ErrNotFound
	}
	return u, nil
}

func (r *fakeUserRepo) ByID(_ context.Context, id string) (domain.User, error) {
	u, ok := r.byID[id]
	if !ok {
		return domain.User{}, errs.ErrNotFound
	}
	return u, nil
}

func (r *fakeUserRepo) SetCustomerID(_ context.Context, userID, customerID string, at time.Time) error {
	u, ok := r.byID[userID]
	if !ok {
		return errs.ErrNotFound
	}
	u.CustomerID = customerID
	u.UpdatedAt = at
	r.byID[userID] = u
	r.byEmail[u.Email] = u
	r.links = append(r.links, linkCall{userID: userID, customerID: customerID, at: at})
	return nil
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

type markRotatedCall struct {
	id          string
	successorID string
	at          time.Time
}

type revokeFamilyCall struct {
	familyID string
	at       time.Time
	reason   string
}

type fakeRefreshRepo struct {
	created map[string]domain.RefreshToken
	byHash  map[string]string
	marked  []markRotatedCall
	revoked []revokeFamilyCall
}

func newFakeRefreshRepo() *fakeRefreshRepo {
	return &fakeRefreshRepo{
		created: map[string]domain.RefreshToken{},
		byHash:  map[string]string{},
	}
}

func (r *fakeRefreshRepo) Create(_ context.Context, token domain.RefreshToken) error {
	r.created[token.ID] = token
	r.byHash[string(token.TokenHash)] = token.ID
	return nil
}

func (r *fakeRefreshRepo) ByHashForUpdate(_ context.Context, tokenHash []byte) (domain.RefreshToken, error) {
	id, ok := r.byHash[string(tokenHash)]
	if !ok {
		return domain.RefreshToken{}, errs.ErrNotFound
	}
	return r.created[id], nil
}

func (r *fakeRefreshRepo) MarkRotated(_ context.Context, id, successorID string, at time.Time) error {
	token := r.created[id]
	token.RotatedAt = &at
	token.ReplacedByID = successorID
	r.created[id] = token
	r.marked = append(r.marked, markRotatedCall{id: id, successorID: successorID, at: at})
	return nil
}

func (r *fakeRefreshRepo) RevokeFamily(_ context.Context, familyID string, at time.Time, reason string) error {
	for id, token := range r.created {
		if token.FamilyID == familyID && token.RevokedAt == nil {
			token.RevokedAt = &at
			token.RevokedReason = reason
			r.created[id] = token
		}
	}
	r.revoked = append(r.revoked, revokeFamilyCall{familyID: familyID, at: at, reason: reason})
	return nil
}

type fakeUnitOfWork struct {
	users           *fakeUserRepo
	refresh         *fakeRefreshRepo
	idempotency     *fakeIdempotencyRepo
	outbox          *fakeOutbox
	processedEvents *fakeProcessedEventsRepo
}

func (u *fakeUnitOfWork) Users() domain.UserRepo                 { return u.users }
func (u *fakeUnitOfWork) RefreshTokens() domain.RefreshTokenRepo { return u.refresh }
func (u *fakeUnitOfWork) Idempotency() domain.IdempotencyRepo    { return u.idempotency }
func (u *fakeUnitOfWork) Outbox() domain.Outbox                  { return u.outbox }
func (u *fakeUnitOfWork) ProcessedEvents() domain.ProcessedEventsRepo {
	return u.processedEvents
}

type fakeTransactor struct{ uow *fakeUnitOfWork }

func (t *fakeTransactor) WithinTx(ctx context.Context, fn func(context.Context, domain.UnitOfWork) error) error {
	return fn(ctx, t.uow)
}

type fakeHasher struct{}

func (fakeHasher) Hash(password string) (string, error) { return "hashed:" + password, nil }

func (fakeHasher) Verify(password, hash string) (bool, error) { return hash == "hashed:"+password, nil }

type sequentialID struct{ n int }

func (s *sequentialID) New() string {
	s.n++
	return fmt.Sprintf("id-%d", s.n)
}

func newRegisterHarness(t *testing.T) (*app.RegisterUseCase, *fakeUserRepo, *fakeOutbox, *fakeIdempotencyRepo) {
	t.Helper()
	return newRegisterHarnessWithMetrics(t, nil)
}

func newRegisterHarnessWithMetrics(t *testing.T, metrics domain.Metrics) (*app.RegisterUseCase, *fakeUserRepo, *fakeOutbox, *fakeIdempotencyRepo) {
	t.Helper()

	users := newFakeUserRepo()
	idempotency := newFakeIdempotencyRepo()
	outbox := &fakeOutbox{}
	uow := &fakeUnitOfWork{
		users:       users,
		refresh:     newFakeRefreshRepo(),
		idempotency: idempotency,
		outbox:      outbox,
	}

	uc := app.NewRegisterUseCase(
		&fakeTransactor{uow: uow},
		fakeHasher{},
		clock.Fixed{T: fixedNow()},
		&sequentialID{},
		metrics,
	)
	return uc, users, outbox, idempotency
}

type fakeMetrics struct {
	registrations int
}

func (m *fakeMetrics) RegistrationCreated(context.Context) { m.registrations++ }

func TestRegister_CountsOnlyNewRegistrations(t *testing.T) {
	t.Parallel()

	metrics := &fakeMetrics{}
	uc, _, _, _ := newRegisterHarnessWithMetrics(t, metrics)
	cmd := app.RegisterCommand{Email: "user@example.com", Password: goodPassword, IdempotencyKey: "key-1"}

	_, err := uc.Register(context.Background(), cmd)
	require.NoError(t, err)
	require.Equal(t, 1, metrics.registrations)

	_, err = uc.Register(context.Background(), cmd)
	require.NoError(t, err)
	require.Equal(t, 1, metrics.registrations, "an idempotent replay must not re-count")
}

func TestRegister_FirstCall_CreatesUserAndEvent(t *testing.T) {
	t.Parallel()

	uc, users, outbox, idempotency := newRegisterHarness(t)

	res, err := uc.Register(context.Background(), app.RegisterCommand{
		Email:          "  User@Example.COM ",
		Password:       goodPassword,
		IdempotencyKey: "key-1",
	})
	require.NoError(t, err)
	require.Equal(t, "id-1", res.UserID)
	require.Equal(t, domain.RoleClient, res.Role)
	require.Empty(t, res.CustomerID, "customer_id is linked asynchronously")

	stored, ok := users.byEmail["user@example.com"]
	require.True(t, ok, "email must be stored normalized")
	require.Equal(t, "id-1", stored.ID)
	require.Equal(t, "hashed:"+goodPassword, stored.PasswordHash)
	require.Equal(t, domain.RoleClient, stored.Role)
	require.Equal(t, fixedNow(), stored.CreatedAt, "time must come only from the injected Clock")
	require.Equal(t, fixedNow(), stored.UpdatedAt)

	require.Len(t, outbox.messages, 1, "exactly one event per registration")
	msg := outbox.messages[0]
	require.Equal(t, events.TopicAuth, msg.Topic)
	require.Equal(t, events.TypeUserRegistered, msg.EventType)
	require.Equal(t, fixedNow(), msg.OccurredAt)

	env, err := events.Decode(msg.Payload)
	require.NoError(t, err)
	require.Equal(t, events.TypeUserRegistered, env.GetEventType())
	require.NotEmpty(t, env.GetEventId())
	require.Equal(t, env.GetEventId(), msg.ID, "the outbox row id is the event id")

	payload := env.GetUserRegistered()
	require.NotNil(t, payload)
	require.Equal(t, "id-1", payload.GetUserId())
	require.Equal(t, "user@example.com", payload.GetEmail())
	require.Equal(t, "CLIENT", payload.GetRole())

	require.Len(t, idempotency.records, 1, "the idempotency row is written in the same tx")
}

func TestRegister_CarriesTraceHeadersToOutbox(t *testing.T) {
	t.Parallel()

	uc, _, outbox, _ := newRegisterHarness(t)

	_, err := uc.Register(context.Background(), app.RegisterCommand{
		Email:          "user@example.com",
		Password:       goodPassword,
		IdempotencyKey: "key-1",
		Headers:        map[string]string{"traceparent": testTraceparent},
	})
	require.NoError(t, err)

	require.Len(t, outbox.messages, 1)
	require.Equal(t, testTraceparent, outbox.messages[0].Headers["traceparent"])
}

func TestRegister_Replay_ReturnsStoredResultWithoutEffects(t *testing.T) {
	t.Parallel()

	uc, users, outbox, idempotency := newRegisterHarness(t)
	cmd := app.RegisterCommand{Email: "user@example.com", Password: goodPassword, IdempotencyKey: "key-1"}

	first, err := uc.Register(context.Background(), cmd)
	require.NoError(t, err)
	require.Len(t, users.byEmail, 1)
	require.Len(t, outbox.messages, 1)

	second, err := uc.Register(context.Background(), cmd)
	require.NoError(t, err)
	require.Equal(t, first, second, "replay returns the stored response")
	require.Len(t, users.byEmail, 1, "replay must not create a second user")
	require.Len(t, outbox.messages, 1, "replay must not double-publish")
	require.Len(t, idempotency.records, 1)
}

func TestRegister_SameKeyDifferentPayload_Conflict(t *testing.T) {
	t.Parallel()

	uc, users, outbox, _ := newRegisterHarness(t)

	_, err := uc.Register(context.Background(), app.RegisterCommand{
		Email: "user@example.com", Password: goodPassword, IdempotencyKey: "key-1",
	})
	require.NoError(t, err)

	_, err = uc.Register(context.Background(), app.RegisterCommand{
		Email: "other@example.com", Password: goodPassword, IdempotencyKey: "key-1",
	})
	require.ErrorIs(t, err, errs.ErrConflict)
	require.Len(t, users.byEmail, 1, "the conflicting attempt must not create a user")
	require.Len(t, outbox.messages, 1, "the conflicting attempt must not enqueue an event")
}

func TestRegister_DuplicateEmail_AlreadyExists(t *testing.T) {
	t.Parallel()

	uc, users, outbox, _ := newRegisterHarness(t)

	_, err := uc.Register(context.Background(), app.RegisterCommand{
		Email: "user@example.com", Password: goodPassword, IdempotencyKey: "key-1",
	})
	require.NoError(t, err)

	_, err = uc.Register(context.Background(), app.RegisterCommand{
		Email: "user@example.com", Password: goodPassword, IdempotencyKey: "key-2",
	})
	require.ErrorIs(t, err, errs.ErrAlreadyExists)
	require.Len(t, users.byEmail, 1)
	require.Len(t, outbox.messages, 1, "the failed attempt must not enqueue an event")
}

func TestRegister_InvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cmd  app.RegisterCommand
	}{
		{
			name: "empty email",
			cmd:  app.RegisterCommand{Email: "", Password: goodPassword, IdempotencyKey: "key-1"},
		},
		{
			name: "malformed email",
			cmd:  app.RegisterCommand{Email: "not-an-email", Password: goodPassword, IdempotencyKey: "key-1"},
		},
		{
			name: "short password",
			cmd:  app.RegisterCommand{Email: "user@example.com", Password: "too-short", IdempotencyKey: "key-1"},
		},
		{
			name: "missing idempotency key",
			cmd:  app.RegisterCommand{Email: "user@example.com", Password: goodPassword, IdempotencyKey: ""},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			uc, users, outbox, idempotency := newRegisterHarness(t)

			_, err := uc.Register(context.Background(), tc.cmd)
			require.ErrorIs(t, err, errs.ErrInvalidArgument)
			require.Empty(t, users.byEmail)
			require.Empty(t, outbox.messages)
			require.Empty(t, idempotency.records)
		})
	}
}
