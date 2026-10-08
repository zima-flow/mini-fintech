package app_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/authn"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/app"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

type fakeTokenVerifier struct {
	claims authn.Claims
	err    error
	calls  int
}

func (f *fakeTokenVerifier) Verify(_ context.Context, _ string) (authn.Claims, error) {
	f.calls++
	if f.err != nil {
		return authn.Claims{}, f.err
	}
	return f.claims, nil
}

func TestValidateToken_Valid(t *testing.T) {
	t.Parallel()

	expiry := fixedNow().Add(time.Hour)
	verifier := &fakeTokenVerifier{claims: authn.Claims{
		Subject:    "user-1",
		Roles:      []string{"OFFICER"},
		CustomerID: "cust-1",
		ExpiresAt:  expiry,
		ID:         "jti-1",
	}}
	uc := app.NewValidateTokenUseCase(verifier)

	res, err := uc.ValidateToken(context.Background(), app.ValidateTokenCommand{AccessToken: "signed"})
	require.NoError(t, err)
	require.Equal(t, "user-1", res.UserID)
	require.Equal(t, domain.RoleOfficer, res.Role)
	require.Equal(t, "cust-1", res.CustomerID)
	require.Equal(t, expiry, res.ExpiresAt)
	require.Equal(t, 1, verifier.calls)
}

func TestValidateToken_Invalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want error
	}{
		{
			name: "bad signature",
			err:  fmt.Errorf("authn: invalid token: %w", errs.ErrUnauthenticated),
			want: errs.ErrUnauthenticated,
		},
		{
			name: "jwks unavailable",
			err:  fmt.Errorf("authn: fetch jwks: %w", errs.ErrUnavailable),
			want: errs.ErrUnavailable,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			uc := app.NewValidateTokenUseCase(&fakeTokenVerifier{err: tc.err})
			_, err := uc.ValidateToken(context.Background(), app.ValidateTokenCommand{AccessToken: "signed"})
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestValidateToken_BadRoleClaim(t *testing.T) {
	t.Parallel()

	tests := map[string][]string{
		"missing": nil,
		"empty":   {""},
		"unknown": {"SUPERUSER"},
		"plural":  {"CLIENT", "ADMIN"},
	}

	for name, roles := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			verifier := &fakeTokenVerifier{claims: authn.Claims{
				Subject:   "user-1",
				Roles:     roles,
				ExpiresAt: fixedNow().Add(time.Hour),
			}}
			uc := app.NewValidateTokenUseCase(verifier)

			_, err := uc.ValidateToken(context.Background(), app.ValidateTokenCommand{AccessToken: "signed"})
			require.ErrorIs(t, err, errs.ErrUnauthenticated)
		})
	}
}
