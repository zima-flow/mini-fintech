package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/clock"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/app"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

func seedRefreshToken(t *testing.T, refresh *fakeRefreshRepo, userID, secret, familyID string, issuedAt, expiresAt time.Time) domain.RefreshToken {
	t.Helper()

	token := domain.RefreshToken{
		ID:        "token-" + secret,
		UserID:    userID,
		TokenHash: domain.HashRefreshToken(secret),
		FamilyID:  familyID,
		IssuedAt:  issuedAt,
		ExpiresAt: expiresAt,
	}
	require.NoError(t, refresh.Create(context.Background(), token))
	return token
}

func newRefreshHarness(t *testing.T) (*app.RefreshUseCase, *fakeUserRepo, *fakeRefreshRepo, *fakeTokenIssuer) {
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

	uc := app.NewRefreshUseCase(
		&fakeTransactor{uow: uow},
		issuer,
		clock.Fixed{T: fixedNow()},
		&sequentialID{},
		app.TokenTTLs{Access: testAccessTTL, Refresh: testRefreshTTL},
	)
	return uc, users, refresh, issuer
}

func newLogoutHarness(t *testing.T) (*app.LogoutUseCase, *fakeRefreshRepo) {
	t.Helper()

	refresh := newFakeRefreshRepo()
	uow := &fakeUnitOfWork{
		users:       newFakeUserRepo(),
		refresh:     refresh,
		idempotency: newFakeIdempotencyRepo(),
		outbox:      &fakeOutbox{},
	}

	uc := app.NewLogoutUseCase(&fakeTransactor{uow: uow}, clock.Fixed{T: fixedNow()})
	return uc, refresh
}

func TestRefresh_Rotation_ReturnsNewPairAndMarksRotated(t *testing.T) {
	t.Parallel()

	uc, users, refresh, issuer := newRefreshHarness(t)
	require.NoError(t, users.Create(context.Background(), loginUser()))
	seed := seedRefreshToken(t, refresh, "user-1", "old-secret", "family-1", fixedNow(), fixedNow().Add(testRefreshTTL))

	res, err := uc.Refresh(context.Background(), app.RefreshCommand{RefreshToken: "old-secret"})
	require.NoError(t, err)
	require.Equal(t, "signed-access-token", res.Tokens.AccessToken)
	require.Equal(t, fixedNow().Add(testAccessTTL), res.Tokens.AccessExpiresAt)

	require.Equal(t, "user-1", issuer.claims.UserID)
	require.Equal(t, domain.RoleClient, issuer.claims.Role)
	require.Equal(t, "cust-1", issuer.claims.CustomerID)
	require.Equal(t, fixedNow(), issuer.claims.IssuedAt)
	require.Equal(t, fixedNow().Add(testAccessTTL), issuer.claims.ExpiresAt)
	require.NotEmpty(t, issuer.claims.TokenID)

	require.Len(t, refresh.marked, 1, "exactly one token is consumed")
	require.Equal(t, seed.ID, refresh.marked[0].id)
	require.Equal(t, fixedNow(), refresh.marked[0].at)

	old := refresh.created[seed.ID]
	require.True(t, old.Rotated())
	require.Equal(t, refresh.marked[0].successorID, old.ReplacedByID)

	successor := refresh.created[refresh.marked[0].successorID]
	require.Equal(t, "family-1", successor.FamilyID, "the successor stays in the same family")
	require.Equal(t, "user-1", successor.UserID)
	require.Equal(t, domain.HashRefreshToken(res.Tokens.RefreshToken), successor.TokenHash)
	require.NotEqual(t, "old-secret", res.Tokens.RefreshToken, "a fresh secret is minted")
	require.Equal(t, fixedNow(), successor.IssuedAt)
	require.Equal(t, fixedNow().Add(testRefreshTTL), successor.ExpiresAt)
	require.False(t, successor.Rotated())
	require.False(t, successor.Revoked())
	require.Empty(t, refresh.revoked)
}

func TestRefresh_Reuse_RevokesFamily(t *testing.T) {
	t.Parallel()

	uc, users, refresh, _ := newRefreshHarness(t)
	require.NoError(t, users.Create(context.Background(), loginUser()))
	seedRefreshToken(t, refresh, "user-1", "old-secret", "family-1", fixedNow(), fixedNow().Add(testRefreshTTL))

	_, err := uc.Refresh(context.Background(), app.RefreshCommand{RefreshToken: "old-secret"})
	require.NoError(t, err)

	_, err = uc.Refresh(context.Background(), app.RefreshCommand{RefreshToken: "old-secret"})
	require.ErrorIs(t, err, errs.ErrUnauthenticated)

	require.Len(t, refresh.revoked, 1)
	require.Equal(t, "family-1", refresh.revoked[0].familyID)
	require.Equal(t, fixedNow(), refresh.revoked[0].at)
	for _, row := range refresh.created {
		if row.FamilyID == "family-1" {
			require.True(t, row.Revoked(), "every token in the family is revoked")
		}
	}
}

func TestRefresh_Expired_Unauthenticated(t *testing.T) {
	t.Parallel()

	uc, users, refresh, issuer := newRefreshHarness(t)
	require.NoError(t, users.Create(context.Background(), loginUser()))
	seedRefreshToken(t, refresh, "user-1", "expired-secret", "family-1", fixedNow().Add(-testRefreshTTL), fixedNow())

	_, err := uc.Refresh(context.Background(), app.RefreshCommand{RefreshToken: "expired-secret"})
	require.ErrorIs(t, err, errs.ErrUnauthenticated)
	require.Empty(t, refresh.marked)
	require.Empty(t, refresh.revoked)
	require.Zero(t, issuer.calls)
}

func TestRefresh_Revoked_RevokesFamily(t *testing.T) {
	t.Parallel()

	uc, users, refresh, _ := newRefreshHarness(t)
	require.NoError(t, users.Create(context.Background(), loginUser()))

	revokedAt := fixedNow().Add(-time.Minute)
	require.NoError(t, refresh.Create(context.Background(), domain.RefreshToken{
		ID:            "token-revoked",
		UserID:        "user-1",
		TokenHash:     domain.HashRefreshToken("revoked-secret"),
		FamilyID:      "family-1",
		IssuedAt:      fixedNow().Add(-time.Hour),
		ExpiresAt:     fixedNow().Add(time.Hour),
		RevokedAt:     &revokedAt,
		RevokedReason: "logout",
	}))

	_, err := uc.Refresh(context.Background(), app.RefreshCommand{RefreshToken: "revoked-secret"})
	require.ErrorIs(t, err, errs.ErrUnauthenticated)
	require.Len(t, refresh.revoked, 1)
	require.Equal(t, "family-1", refresh.revoked[0].familyID)
}

func TestRefresh_UnknownToken_Unauthenticated(t *testing.T) {
	t.Parallel()

	uc, _, refresh, issuer := newRefreshHarness(t)

	_, err := uc.Refresh(context.Background(), app.RefreshCommand{RefreshToken: "never-issued"})
	require.ErrorIs(t, err, errs.ErrUnauthenticated)
	require.Empty(t, refresh.marked)
	require.Empty(t, refresh.revoked)
	require.Zero(t, issuer.calls)
}

func TestLogout_RevokesFamily(t *testing.T) {
	t.Parallel()

	uc, refresh := newLogoutHarness(t)
	seed := seedRefreshToken(t, refresh, "user-1", "logout-secret", "family-1", fixedNow().Add(-time.Hour), fixedNow().Add(time.Hour))

	require.NoError(t, uc.Logout(context.Background(), app.LogoutCommand{RefreshToken: "logout-secret"}))

	require.Len(t, refresh.revoked, 1)
	require.Equal(t, "family-1", refresh.revoked[0].familyID)
	require.Equal(t, fixedNow(), refresh.revoked[0].at)
	require.True(t, refresh.created[seed.ID].Revoked())
}

func TestLogout_UnknownToken_Unauthenticated(t *testing.T) {
	t.Parallel()

	uc, refresh := newLogoutHarness(t)

	err := uc.Logout(context.Background(), app.LogoutCommand{RefreshToken: "never-issued"})
	require.ErrorIs(t, err, errs.ErrUnauthenticated)
	require.Empty(t, refresh.revoked)
}
