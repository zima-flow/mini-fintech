package app_test

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/clock"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/app"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

const (
	testAccessTTL  = 15 * time.Minute
	testRefreshTTL = 30 * 24 * time.Hour
)

type fakeTokenIssuer struct {
	token  string
	claims domain.AccessClaims
	calls  int
}

func (f *fakeTokenIssuer) IssueAccess(_ context.Context, claims domain.AccessClaims) (string, error) {
	f.claims = claims
	f.calls++
	return f.token, nil
}

func loginUser() domain.User {
	return domain.User{
		ID:           "user-1",
		Email:        "user@example.com",
		PasswordHash: "hashed:" + goodPassword,
		Role:         domain.RoleClient,
		CustomerID:   "cust-1",
	}
}

func newLoginHarness(t *testing.T) (*app.LoginUseCase, *fakeUserRepo, *fakeRefreshRepo, *fakeTokenIssuer) {
	t.Helper()

	users := newFakeUserRepo()
	refresh := newFakeRefreshRepo()
	issuer := &fakeTokenIssuer{token: "signed-access-token"}
	uow := &fakeUnitOfWork{
		users:       users,
		refresh:     refresh,
		idempotency: newFakeIdempotencyRepo(),
		outbox:      &fakeOutbox{},
	}

	uc := app.NewLoginUseCase(
		&fakeTransactor{uow: uow},
		fakeHasher{},
		issuer,
		clock.Fixed{T: fixedNow()},
		&sequentialID{},
		app.TokenTTLs{Access: testAccessTTL, Refresh: testRefreshTTL},
	)
	return uc, users, refresh, issuer
}

func TestLogin_Success_ReturnsPairAndStoresHashedRefresh(t *testing.T) {
	t.Parallel()

	uc, users, refresh, issuer := newLoginHarness(t)
	require.NoError(t, users.Create(context.Background(), loginUser()))

	res, err := uc.Login(context.Background(), app.LoginCommand{
		Email:    "  User@Example.COM ",
		Password: goodPassword,
	})
	require.NoError(t, err)
	require.Equal(t, "user-1", res.UserID)
	require.Equal(t, domain.RoleClient, res.Role)
	require.Equal(t, "cust-1", res.CustomerID)
	require.Equal(t, "signed-access-token", res.Tokens.AccessToken)
	require.Equal(t, fixedNow().Add(testAccessTTL), res.Tokens.AccessExpiresAt)

	require.Equal(t, 1, issuer.calls)
	require.Equal(t, "user-1", issuer.claims.UserID)
	require.Equal(t, domain.RoleClient, issuer.claims.Role)
	require.Equal(t, "cust-1", issuer.claims.CustomerID)
	require.Equal(t, fixedNow(), issuer.claims.IssuedAt)
	require.Equal(t, fixedNow().Add(testAccessTTL), issuer.claims.ExpiresAt)
	require.NotEmpty(t, issuer.claims.TokenID, "the access token carries a jti")

	raw, err := base64.RawURLEncoding.DecodeString(res.Tokens.RefreshToken)
	require.NoError(t, err)
	require.Len(t, raw, 32)

	require.Len(t, refresh.created, 1)
	var stored domain.RefreshToken
	for _, row := range refresh.created {
		stored = row
	}
	require.NotEmpty(t, stored.ID)
	require.NotEmpty(t, stored.FamilyID)
	require.Equal(t, "user-1", stored.UserID)
	require.Equal(t, domain.HashRefreshToken(res.Tokens.RefreshToken), stored.TokenHash)
	require.NotEqual(t, res.Tokens.RefreshToken, string(stored.TokenHash))
	require.Equal(t, fixedNow(), stored.IssuedAt)
	require.Equal(t, fixedNow().Add(testRefreshTTL), stored.ExpiresAt)
	require.False(t, stored.Rotated())
	require.False(t, stored.Revoked())
}

func TestLogin_InvalidCredentials_Unauthenticated(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		email    string
		password string
	}{
		{name: "unknown email", email: "nobody@example.com", password: goodPassword},
		{name: "bad password", email: "user@example.com", password: "not-the-password"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			uc, users, refresh, issuer := newLoginHarness(t)
			require.NoError(t, users.Create(context.Background(), loginUser()))

			_, err := uc.Login(context.Background(), app.LoginCommand{
				Email:    tc.email,
				Password: tc.password,
			})
			require.ErrorIs(t, err, errs.ErrUnauthenticated)
			require.Empty(t, refresh.created, "a failed login must store no token")
			require.Zero(t, issuer.calls, "a failed login must issue no token")
		})
	}
}

func TestLogin_FailuresAreIndistinguishable(t *testing.T) {
	t.Parallel()

	uc, users, _, _ := newLoginHarness(t)
	require.NoError(t, users.Create(context.Background(), loginUser()))

	_, badPassword := uc.Login(context.Background(), app.LoginCommand{
		Email: "user@example.com", Password: "not-the-password",
	})
	_, unknownEmail := uc.Login(context.Background(), app.LoginCommand{
		Email: "nobody@example.com", Password: goodPassword,
	})

	require.ErrorIs(t, badPassword, errs.ErrUnauthenticated)
	require.ErrorIs(t, unknownEmail, errs.ErrUnauthenticated)
	require.Equal(t, badPassword.Error(), unknownEmail.Error())
}
