package app_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/clock"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/events"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/app"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

func newOfficerHarness(t *testing.T) (*app.CreateOfficerUseCase, *fakeUserRepo, *fakeOutbox, *fakeIdempotencyRepo) {
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

	uc := app.NewCreateOfficerUseCase(
		&fakeTransactor{uow: uow},
		fakeHasher{},
		clock.Fixed{T: fixedNow()},
		&sequentialID{},
	)
	return uc, users, outbox, idempotency
}

func TestCreateOfficer_AdminCreatesOfficer(t *testing.T) {
	t.Parallel()

	uc, users, outbox, idempotency := newOfficerHarness(t)

	res, err := uc.CreateOfficer(context.Background(), app.CreateOfficerCommand{
		Email:          " Officer@Example.COM ",
		Password:       goodPassword,
		IdempotencyKey: "key-1",
		ActorRole:      domain.RoleAdmin,
	})
	require.NoError(t, err)
	require.Equal(t, "id-1", res.UserID)
	require.Equal(t, domain.RoleOfficer, res.Role)

	stored, ok := users.byEmail["officer@example.com"]
	require.True(t, ok, "email must be stored normalized")
	require.Equal(t, domain.RoleOfficer, stored.Role)
	require.Equal(t, "hashed:"+goodPassword, stored.PasswordHash)
	require.Equal(t, fixedNow(), stored.CreatedAt)

	require.Len(t, outbox.messages, 1)
	env, err := events.Decode(outbox.messages[0].Payload)
	require.NoError(t, err)
	require.Equal(t, "id-1", env.GetUserRegistered().GetUserId())
	require.Equal(t, "officer@example.com", env.GetUserRegistered().GetEmail())
	require.Equal(t, "OFFICER", env.GetUserRegistered().GetRole())

	require.Len(t, idempotency.records, 1)
}

func TestCreateOfficer_NonAdminDenied(t *testing.T) {
	t.Parallel()

	for _, role := range []domain.Role{domain.RoleClient, domain.RoleOfficer} {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			uc, users, outbox, idempotency := newOfficerHarness(t)

			_, err := uc.CreateOfficer(context.Background(), app.CreateOfficerCommand{
				Email:          "officer@example.com",
				Password:       goodPassword,
				IdempotencyKey: "key-1",
				ActorRole:      role,
			})
			require.ErrorIs(t, err, errs.ErrPermissionDenied)
			require.Empty(t, users.byEmail)
			require.Empty(t, outbox.messages)
			require.Empty(t, idempotency.records)
		})
	}
}

func TestCreateOfficer_IdempotentReplay(t *testing.T) {
	t.Parallel()

	uc, users, outbox, _ := newOfficerHarness(t)
	cmd := app.CreateOfficerCommand{
		Email: "officer@example.com", Password: goodPassword,
		IdempotencyKey: "key-1", ActorRole: domain.RoleAdmin,
	}

	first, err := uc.CreateOfficer(context.Background(), cmd)
	require.NoError(t, err)
	second, err := uc.CreateOfficer(context.Background(), cmd)
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.Len(t, users.byEmail, 1, "replay must not create a second account")
	require.Len(t, outbox.messages, 1, "replay must not double-publish")
}

func TestCreateOfficer_SameKeyDifferentPayload_Conflict(t *testing.T) {
	t.Parallel()

	uc, users, outbox, _ := newOfficerHarness(t)

	_, err := uc.CreateOfficer(context.Background(), app.CreateOfficerCommand{
		Email: "officer@example.com", Password: goodPassword,
		IdempotencyKey: "key-1", ActorRole: domain.RoleAdmin,
	})
	require.NoError(t, err)

	_, err = uc.CreateOfficer(context.Background(), app.CreateOfficerCommand{
		Email: "other@example.com", Password: goodPassword,
		IdempotencyKey: "key-1", ActorRole: domain.RoleAdmin,
	})
	require.ErrorIs(t, err, errs.ErrConflict)
	require.Len(t, users.byEmail, 1)
	require.Len(t, outbox.messages, 1)
}

func TestCreateOfficer_DuplicateEmail_AlreadyExists(t *testing.T) {
	t.Parallel()

	uc, users, outbox, _ := newOfficerHarness(t)

	_, err := uc.CreateOfficer(context.Background(), app.CreateOfficerCommand{
		Email: "officer@example.com", Password: goodPassword,
		IdempotencyKey: "key-1", ActorRole: domain.RoleAdmin,
	})
	require.NoError(t, err)

	_, err = uc.CreateOfficer(context.Background(), app.CreateOfficerCommand{
		Email: "officer@example.com", Password: goodPassword,
		IdempotencyKey: "key-2", ActorRole: domain.RoleAdmin,
	})
	require.ErrorIs(t, err, errs.ErrAlreadyExists)
	require.Len(t, users.byEmail, 1)
	require.Len(t, outbox.messages, 1)
}

func TestCreateOfficer_InvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cmd  app.CreateOfficerCommand
	}{
		{
			name: "bad email",
			cmd:  app.CreateOfficerCommand{Email: "not-an-email", Password: goodPassword, IdempotencyKey: "key-1", ActorRole: domain.RoleAdmin},
		},
		{
			name: "short password",
			cmd:  app.CreateOfficerCommand{Email: "officer@example.com", Password: "too-short", IdempotencyKey: "key-1", ActorRole: domain.RoleAdmin},
		},
		{
			name: "missing idempotency key",
			cmd:  app.CreateOfficerCommand{Email: "officer@example.com", Password: goodPassword, IdempotencyKey: "", ActorRole: domain.RoleAdmin},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			uc, users, outbox, idempotency := newOfficerHarness(t)

			_, err := uc.CreateOfficer(context.Background(), tc.cmd)
			require.ErrorIs(t, err, errs.ErrInvalidArgument)
			require.Empty(t, users.byEmail)
			require.Empty(t, outbox.messages)
			require.Empty(t, idempotency.records)
		})
	}
}
